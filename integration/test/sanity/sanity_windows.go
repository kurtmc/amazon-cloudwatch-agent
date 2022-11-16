// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

//go:build windows
// +build windows

package sanity

import (
	"testing"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/integration/test"
)

func SanityCheck(t *testing.T) {
	err := test.RunShellScript("resources/verifyWindowsCtlScript.ps1")
	if err != nil {
		t.Fatalf("Running sanity check failed")
	}
}