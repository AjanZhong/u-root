// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestValidateTransport(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr error
	}{
		{in: "tcp"},
		{in: "TCP"},
		{in: "rdma"},
		{in: "fc", wantErr: errNeedTransport},
		{in: "", wantErr: errNeedTransport},
	} {
		err := validateTransport(tc.in)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("validateTransport(%q) = %v, want %v", tc.in, err, tc.wantErr)
		}
	}
}

func TestValidateAddr(t *testing.T) {
	if err := validateAddr("192.168.2.218"); err != nil {
		t.Errorf("valid IPv4: %v", err)
	}
	if err := validateAddr("2001:db8::1"); err != nil {
		t.Errorf("valid IPv6: %v", err)
	}
	if err := validateAddr("nvmf.example.com"); !errors.Is(err, errNeedIP) {
		t.Errorf("hostname: got %v, want %v", err, errNeedIP)
	}
	if err := validateAddr("999.1.1.1"); !errors.Is(err, errNeedIP) {
		t.Errorf("invalid IP: got %v, want %v", err, errNeedIP)
	}
}

func TestValidatePort(t *testing.T) {
	if err := validatePort("44420"); err != nil {
		t.Errorf("valid port: %v", err)
	}
	if err := validatePort("0"); !errors.Is(err, errNeedPort) {
		t.Errorf("port 0: got %v, want %v", err, errNeedPort)
	}
	if err := validatePort("65536"); !errors.Is(err, errNeedPort) {
		t.Errorf("port 65536: got %v, want %v", err, errNeedPort)
	}
	if err := validatePort("abc"); !errors.Is(err, errNeedPort) {
		t.Errorf("non-numeric: got %v, want %v", err, errNeedPort)
	}
}

func TestParseConnectedDevice(t *testing.T) {
	got, err := parseConnectedDevice("connected: /dev/nvme0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/dev/nvme0" {
		t.Errorf("got %q, want /dev/nvme0", got)
	}
	got, err = parseConnectedDevice("connected: /dev/nvme12")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/dev/nvme12" {
		t.Errorf("got %q, want /dev/nvme12", got)
	}
	if _, err := parseConnectedDevice("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaultPartition(t *testing.T) {
	got := defaultPartition("/dev/nvme0", defaultDevice)
	if got != "/dev/nvme0n1p4" {
		t.Errorf("got %q, want /dev/nvme0n1p4", got)
	}
	got = defaultPartition("/dev/nvme3", defaultDevice)
	if got != "/dev/nvme3n1p4" {
		t.Errorf("got %q, want /dev/nvme3n1p4", got)
	}
	got = defaultPartition("not-nvme", defaultDevice)
	if got != defaultDevice {
		t.Errorf("got %q, want %q", got, defaultDevice)
	}
}

func TestPromptString(t *testing.T) {
	var out bytes.Buffer
	r := bufio.NewReader(strings.NewReader("\n"))
	got, err := promptString(r, &out, "NVMe-oF address", defaultAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultAddr {
		t.Errorf("empty input: got %q, want default %q", got, defaultAddr)
	}

	out.Reset()
	r = bufio.NewReader(strings.NewReader("10.0.0.2\n"))
	got, err = promptString(r, &out, "NVMe-oF address", defaultAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.2" {
		t.Errorf("override: got %q, want 10.0.0.2", got)
	}
}

func TestPromptUntilYesUsesDefault(t *testing.T) {
	c := command(strings.NewReader(""), io.Discard, io.Discard, &options{yes: true})
	got, err := c.promptUntil(bufio.NewReader(c.stdin), "port", "44420", validatePort)
	if err != nil {
		t.Fatal(err)
	}
	if got != "44420" {
		t.Errorf("got %q, want 44420", got)
	}
}

func TestFlagDefaults(t *testing.T) {
	o := &options{}
	fs := flag.NewFlagSet("nvmfboot", flag.ContinueOnError)
	o.register(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.transport != defaultTransport {
		t.Errorf("transport = %q", o.transport)
	}
	if o.traddr != defaultAddr {
		t.Errorf("traddr = %q", o.traddr)
	}
	if o.trsvcid != defaultPort {
		t.Errorf("trsvcid = %q", o.trsvcid)
	}
	if o.nqn != defaultNQN {
		t.Errorf("nqn = %q", o.nqn)
	}
	if o.hostnqn != defaultHostNQN {
		t.Errorf("hostnqn = %q", o.hostnqn)
	}
	if o.device != defaultDevice {
		t.Errorf("device = %q", o.device)
	}
	if o.mountPoint != defaultMountPoint {
		t.Errorf("mountPoint = %q", o.mountPoint)
	}
	if o.init != defaultInit {
		t.Errorf("init = %q", o.init)
	}
}

func TestExecutingMessage(t *testing.T) {
	var out bytes.Buffer
	c := command(strings.NewReader(""), &out, io.Discard, &options{})
	c.executing("mkdir -p /run")
	got := out.String()
	want := "nvmfboot: executing: mkdir -p /run\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNVMeConnectArgs(t *testing.T) {
	o := &options{
		transport: "tcp",
		traddr:    defaultAddr,
		trsvcid:   defaultPort,
		nqn:       defaultNQN,
		hostnqn:   defaultHostNQN,
	}
	got := strings.Join(o.nvmeConnectArgs(), " ")
	want := "connect -t tcp -a 192.168.2.218 -s 44420 -n nqn.2026-03.spdk.newfw:3a0b838a-c8fe-4955-a76b-d6767c376f9e -hostnqn nqn.2014-08.org.nvmexpress:uuid:035e02d8-04d3-0580-2a06-d40700080009"
	if got != want {
		t.Errorf("nvmeConnectArgs() = %q\nwant %q", got, want)
	}
}
