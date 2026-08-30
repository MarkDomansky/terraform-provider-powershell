package scriptprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"

	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// Manifest filenames inside the provider/ tree. They are deliberately
// distinctive rather than a generic schema.json so that editors can associate
// the published JSON Schemas (https://json.schemastore.org/tfpowershell-*.json)
// by filename alone, with no per-file $schema key and no directory-scoped glob.
const (
	// Provider-level files, at fixed paths.
	providerSettingsPath = "provider/settings.tfps.json"
	providerManifestPath = "provider/provider.tfps.json"

	// Per-folder manifests, one inside each provider/resources/<name>/ and
	// provider/data-sources/<name>/ directory.
	resourceManifestName   = "resource.tfps.json"
	dataSourceManifestName = "datasource.tfps.json"
)

// Settings identifies a definition-based (derived) provider. It is authored by
// the fork as provider/settings.tfps.json and read from the embedded filesystem.
type Settings struct {
	// Name is the provider type name, e.g. "exchangeonlinemanagement". It
	// prefixes every resource and data source type.
	Name string `json:"name"`
	// Address is the registry source address practitioners put in
	// required_providers, e.g. "registry.terraform.io/acme/exchangeonlinemanagement".
	Address string `json:"address"`
}

// Definition is everything needed to serve a derived provider: its identity,
// version, and the embedded filesystem containing the provider/ tree
// (settings, lifecycle scripts, resource and data-source folders).
type Definition struct {
	Settings Settings
	Version  string // stamped at build time via -ldflags "-X main.version=..."
	FS       fs.FS  // tree containing "provider/..." (typically an embed.FS)
	Debug    bool   // run with debugger support (managed by the fork's main.go flag)
}

// ScriptSet holds the CRUD script contents for one resource. Update == ""
// means the resource has no update.ps1: any config change forces replacement.
type ScriptSet struct {
	Create string
	Read   string
	Update string
	Delete string
}

// ResourceDefinition is one discovered provider/resources/<name>/ folder,
// parsed and schema-built at load time.
type ResourceDefinition struct {
	Name     string // directory name; Terraform type is <provider>_<Name>
	Manifest *Manifest
	Schema   rschema.Schema
	Scripts  ScriptSet
	Timeout  time.Duration // from timeout_seconds; 0 means provider default
}

// DataSourceDefinition is one discovered provider/data-sources/<name>/ folder.
type DataSourceDefinition struct {
	Name       string
	Manifest   *Manifest
	Schema     dschema.Schema
	ReadScript string
	Timeout    time.Duration
}

// providerDefinition is the fully loaded, validated definition a
// definitionProvider serves from.
type providerDefinition struct {
	settings         Settings
	version          string
	providerManifest *Manifest // nil when provider/provider.tfps.json is absent
	customAttrs      map[string]pschema.Attribute
	startupScript    string // provider/scripts/startup.ps1, "" when absent
	shutdownScript   string // provider/scripts/shutdown.ps1, "" when absent
	resources        []*ResourceDefinition
	dataSources      []*DataSourceDefinition
}

// Serve loads the definition and serves the provider until Terraform closes
// the plugin, then tears down every provider instance (running shutdown
// scripts and stopping sidecars). It blocks; load errors and serve errors are
// returned.
func Serve(ctx context.Context, def Definition) error {
	newProvider, factory, err := NewProviderFactory(def)
	if err != nil {
		return err
	}
	serveErr := providerserver.Serve(ctx, newProvider, providerserver.ServeOpts{
		Address: def.Settings.Address,
		Debug:   def.Debug,
	})
	// Teardown must run even when Serve returns an error, and must not reuse a
	// context that Serve's caller may have cancelled.
	factory.Shutdown(context.Background())
	return serveErr
}

// NewProviderFactory loads and validates the definition eagerly (bad manifests
// or missing scripts fail here, not at first use) and returns a provider
// constructor plus the tracking Factory whose Shutdown tears down every
// instance. Intended for Serve and for acceptance tests via
// providerserver.NewProtocol6WithError.
func NewProviderFactory(def Definition) (func() provider.Provider, *Factory, error) {
	pd, err := loadDefinition(def)
	if err != nil {
		return nil, nil, err
	}
	factory := NewFactoryWith(func() ShutdownProvider {
		return &definitionProvider{def: pd}
	})
	return factory.New(), factory, nil
}

// loadDefinition walks the provider/ tree, parses every manifest, reads every
// script, and builds the framework schemas. All access uses fs.FS forward-slash
// paths (never filepath), so behavior is identical on Windows.
func loadDefinition(def Definition) (*providerDefinition, error) {
	if !attrNamePattern.MatchString(def.Settings.Name) {
		return nil, fmt.Errorf(`settings: "name" %q must match %s`, def.Settings.Name, attrNamePattern.String())
	}
	if def.Settings.Address == "" {
		return nil, fmt.Errorf(`settings: "address" is required (e.g. "registry.terraform.io/acme/%s")`, def.Settings.Name)
	}
	if def.FS == nil {
		return nil, fmt.Errorf("definition: FS is required")
	}

	pd := &providerDefinition{
		settings: def.Settings,
		version:  def.Version,
	}

	// Optional provider-level manifest (custom provider-block attributes).
	if data, err := readOptionalFile(def.FS, providerManifestPath); err != nil {
		return nil, err
	} else if data != nil {
		m, err := parseManifest(data, manifestProvider, providerManifestPath)
		if err != nil {
			return nil, err
		}
		builtin := builtinProviderAttributes()
		for name := range m.Attributes {
			if _, taken := builtin[name]; taken {
				return nil, fmt.Errorf("%s: attribute %q collides with a built-in provider attribute", providerManifestPath, name)
			}
		}
		attrs, err := buildProviderAttributes(m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", providerManifestPath, err)
		}
		pd.providerManifest = m
		pd.customAttrs = attrs
	}

	// Optional provider lifecycle scripts.
	startup, err := readOptionalScript(def.FS, "provider/scripts/startup.ps1")
	if err != nil {
		return nil, err
	}
	pd.startupScript = startup
	shutdown, err := readOptionalScript(def.FS, "provider/scripts/shutdown.ps1")
	if err != nil {
		return nil, err
	}
	pd.shutdownScript = shutdown

	// Resources.
	resourceDirs, err := listDefinitionDirs(def.FS, "provider/resources")
	if err != nil {
		return nil, err
	}
	for _, name := range resourceDirs {
		rd, err := loadResourceDefinition(def.FS, name)
		if err != nil {
			return nil, err
		}
		pd.resources = append(pd.resources, rd)
	}

	// Data sources.
	dataSourceDirs, err := listDefinitionDirs(def.FS, "provider/data-sources")
	if err != nil {
		return nil, err
	}
	for _, name := range dataSourceDirs {
		dd, err := loadDataSourceDefinition(def.FS, name)
		if err != nil {
			return nil, err
		}
		pd.dataSources = append(pd.dataSources, dd)
	}

	return pd, nil
}

// LoadSettings reads and validates provider/settings.tfps.json from the given
// filesystem. The fork's managed main.go uses this so the identity lives in
// exactly one user-owned file.
func LoadSettings(fsys fs.FS) (Settings, error) {
	data, err := fs.ReadFile(fsys, providerSettingsPath)
	if err != nil {
		return Settings{}, fmt.Errorf("%s: %w", providerSettingsPath, err)
	}
	var raw struct {
		// Schema is the editor's JSON Schema pointer. It carries no provider
		// semantics; it is declared only so strict decoding accepts it.
		Schema        string `json:"$schema"`
		Name          string `json:"name"`
		Address       string `json:"address"`
		Repository    string `json:"repository"`
		EngineVersion string `json:"engine_version"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", providerSettingsPath, err)
	}
	s := Settings{Name: raw.Name, Address: raw.Address}
	if !attrNamePattern.MatchString(s.Name) {
		return Settings{}, fmt.Errorf(`%s: "name" %q must match %s`, providerSettingsPath, s.Name, attrNamePattern.String())
	}
	if s.Address == "" {
		return Settings{}, fmt.Errorf(`%s: "address" is required`, providerSettingsPath)
	}
	return s, nil
}

// loadResourceDefinition loads provider/resources/<name>/: resource.tfps.json
// plus create/read/delete.ps1 (required) and update.ps1 (optional — its
// presence alone decides update-vs-replace semantics).
func loadResourceDefinition(fsys fs.FS, name string) (*ResourceDefinition, error) {
	dir := path.Join("provider/resources", name)
	if !attrNamePattern.MatchString(name) {
		return nil, fmt.Errorf("%s: resource directory name must match %s", dir, attrNamePattern.String())
	}

	manifestPath := path.Join(dir, resourceManifestName)
	data, err := fs.ReadFile(fsys, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%s: every resource folder needs a %s: %w", dir, resourceManifestName, err)
	}
	m, err := parseManifest(data, manifestResource, manifestPath)
	if err != nil {
		return nil, err
	}

	schema, err := buildResourceSchema(m)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}

	var scripts ScriptSet
	for _, req := range []struct {
		file   string
		target *string
	}{
		{"create.ps1", &scripts.Create},
		{"read.ps1", &scripts.Read},
		{"delete.ps1", &scripts.Delete},
	} {
		content, err := fs.ReadFile(fsys, path.Join(dir, req.file))
		if err != nil {
			return nil, fmt.Errorf("%s: missing required script %s: %w", dir, req.file, err)
		}
		*req.target = string(content)
	}
	update, err := readOptionalScript(fsys, path.Join(dir, "update.ps1"))
	if err != nil {
		return nil, err
	}
	scripts.Update = update

	return &ResourceDefinition{
		Name:     name,
		Manifest: m,
		Schema:   schema,
		Scripts:  scripts,
		Timeout:  time.Duration(m.TimeoutSeconds) * time.Second,
	}, nil
}

// loadDataSourceDefinition loads provider/data-sources/<name>/:
// datasource.tfps.json plus read.ps1.
func loadDataSourceDefinition(fsys fs.FS, name string) (*DataSourceDefinition, error) {
	dir := path.Join("provider/data-sources", name)
	if !attrNamePattern.MatchString(name) {
		return nil, fmt.Errorf("%s: data source directory name must match %s", dir, attrNamePattern.String())
	}

	manifestPath := path.Join(dir, dataSourceManifestName)
	data, err := fs.ReadFile(fsys, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%s: every data-source folder needs a %s: %w", dir, dataSourceManifestName, err)
	}
	m, err := parseManifest(data, manifestDataSource, manifestPath)
	if err != nil {
		return nil, err
	}

	schema, err := buildDataSourceSchema(m)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}

	read, err := fs.ReadFile(fsys, path.Join(dir, "read.ps1"))
	if err != nil {
		return nil, fmt.Errorf("%s: missing required script read.ps1: %w", dir, err)
	}

	return &DataSourceDefinition{
		Name:       name,
		Manifest:   m,
		Schema:     schema,
		ReadScript: string(read),
		Timeout:    time.Duration(m.TimeoutSeconds) * time.Second,
	}, nil
}

// listDefinitionDirs returns the sorted subdirectory names of root, skipping
// non-directories. A missing root is fine (a provider may have no resources or
// no data sources).
func listDefinitionDirs(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		// Missing directory: nothing declared. Any other error is real.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readOptionalFile returns the file's bytes, or nil (no error) when it does
// not exist.
func readOptionalFile(fsys fs.FS, name string) ([]byte, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return data, nil
}

// readOptionalScript returns the file's content, or "" when it does not exist.
func readOptionalScript(fsys fs.FS, name string) (string, error) {
	data, err := readOptionalFile(fsys, name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
