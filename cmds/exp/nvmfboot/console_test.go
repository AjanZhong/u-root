// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"reflect"
	"testing"
)

func TestMergeConsoleNames(t *testing.T) {
	tests := []struct {
		name string
		cmd  []string
		want []string
	}{
		{
			name: "serial and tty from cmdline",
			cmd:  []string{"tty0", "ttyS0"},
			want: []string{"tty0", "ttyS0"},
		},
		{
			name: "serial only still includes tty0 for USB keyboard",
			cmd:  []string{"ttyS0"},
			want: []string{"ttyS0", "tty0"},
		},
		{
			name: "empty cmdline includes tty0 and ttyS0",
			cmd:  nil,
			want: []string{"tty0", "ttyS0"},
		},
		{
			name: "duplicates dropped",
			cmd:  []string{"tty0", "tty0", "ttyS0"},
			want: []string{"tty0", "ttyS0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeConsoleNames(tt.cmd)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeConsoleNames(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}
