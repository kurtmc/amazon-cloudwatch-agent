// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/receiver/adapter"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/common"
)

func TestTranslator(t *testing.T) {
	testCases := map[string]struct {
		input        map[string]interface{}
		cfgType      string
		cfgKey       string
		wantErr      error
		wantInterval time.Duration
	}{
		"WithoutKeyInConfig": {
			input:   map[string]interface{}{},
			cfgType: "test",
			cfgKey:  "mem",
			wantErr: &common.MissingKeyError{Type: "telegraf_test", JsonKey: "mem"},
		},
		"WithoutIntervalInSection": {
			input: map[string]interface{}{
				"metrics": map[string]interface{}{
					"metrics_collected": map[string]interface{}{
						"cpu": map[string]interface{}{},
					},
				},
			},
			cfgType:      "test",
			cfgKey:       "metrics::metrics_collected::cpu",
			wantInterval: time.Minute,
		},
		"WithValidConfig": {
			input: map[string]interface{}{
				"metrics": map[string]interface{}{
					"metrics_collected": map[string]interface{}{
						"mem": map[string]interface{}{
							"measurement":                 []string{"mem_used_percent"},
							"metrics_collection_interval": "20s",
						},
					},
				},
			},
			cfgType:      "test",
			cfgKey:       "metrics::metrics_collected::mem",
			wantInterval: 20 * time.Second,
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			conf := confmap.NewFromStringMap(testCase.input)
			tt := NewTranslator(testCase.cfgType, testCase.cfgKey)
			got, err := tt.Translate(conf)
			require.Equal(t, testCase.wantErr, err)
			if err == nil {
				require.NotNil(t, got)
				gotCfg, ok := got.(*adapter.Config)
				require.True(t, ok)
				require.Equal(t, adapter.Type(testCase.cfgType), gotCfg.ID().Type())
				require.Equal(t, testCase.wantInterval, gotCfg.CollectionInterval)
			}
		})
	}
}

func TestGetCollectionInterval(t *testing.T) {
	sectionKeys := []string{"section", "backup"}
	testCases := map[string]struct {
		inputConfig map[string]interface{}
		want        time.Duration
	}{
		"WithDefault": {
			inputConfig: map[string]interface{}{},
			want:        time.Minute,
		},
		"WithoutSectionOverride": {
			inputConfig: map[string]interface{}{
				"backup": map[string]interface{}{
					"metrics_collection_interval": 10,
				},
				"section": map[string]interface{}{},
			},
			want: 10 * time.Second,
		},
		"WithInvalidSectionOverride": {
			inputConfig: map[string]interface{}{
				"backup": map[string]interface{}{
					"metrics_collection_interval": 10,
				},
				"section": map[string]interface{}{
					"metrics_collection_interval": "invalid",
				},
			},
			want: 10 * time.Second,
		},
		"WithSectionOverride": {
			inputConfig: map[string]interface{}{
				"backup": map[string]interface{}{
					"metrics_collection_interval": 10,
				},
				"section": map[string]interface{}{
					"metrics_collection_interval": 120,
				},
			},
			want: 2 * time.Minute,
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			conf := confmap.NewFromStringMap(testCase.inputConfig)
			got := getCollectionInterval(conf, sectionKeys)
			require.Equal(t, testCase.want, got)
		})
	}
}