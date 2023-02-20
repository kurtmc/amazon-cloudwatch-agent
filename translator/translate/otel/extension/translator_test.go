// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package extension

import (
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/common"
)

type testTranslator struct {
	result component.Config
}

var _ common.Translator[component.Config] = (*testTranslator)(nil)

func (t testTranslator) Translate(_ *confmap.Conf, _ common.TranslatorOptions) (component.Config, error) {
	return t.result, nil
}

func (t testTranslator) Type() component.Type {
	return ""
}

func TestTranslator(t *testing.T) {
	et := NewTranslator()
	require.EqualValues(t, "", et.Type())
	got, err := et.Translate(confmap.New(), common.TranslatorOptions{})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got, 0)
	et = NewTranslator(
		&testTranslator{
			result: componenttest.NewNopExtensionFactory().CreateDefaultConfig(),
		},
	)
	got, err = et.Translate(confmap.New(), common.TranslatorOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
}