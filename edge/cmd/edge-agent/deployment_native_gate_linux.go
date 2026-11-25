//go:build linux && hex_deployment_native_acceptance

package main

import (
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/setup"
	"os"
)

func init() {
	if len(os.Args) == 2 && os.Args[1] == "-native-stopped-deployment-acceptance" {
		if e := setup.RunNativeStoppedDeploymentAcceptance(context.Background(), os.Stdin); e != nil {
			fmt.Fprintln(os.Stderr, "native stopped deployment acceptance refused")
			os.Exit(1)
		}
		fmt.Println("native stopped deployment payload/access/state/unit verified; no start or enable")
		os.Exit(0)
	}
}
