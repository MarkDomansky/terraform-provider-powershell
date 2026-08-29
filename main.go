package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/markdomansky/terraform-provider-powershell/scriptprovider"
)

var version = "0.1-dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/markdomansky/powershell",
		Debug:   debug,
	}

	// Use a tracking factory so we can run each provider's shutdown script and
	// stop its PowerShell process once Serve returns (i.e. the run is over).
	factory := scriptprovider.NewFactory(version)

	err := providerserver.Serve(context.Background(), factory.New(), opts)

	factory.Shutdown(context.Background())

	if err != nil {
		log.Fatal(err.Error())
	}
}
