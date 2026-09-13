// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"
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
	c := command(strings.NewReader("\n"), &out, io.Discard, &options{timeout: time.Second})
	got, err := c.promptString(newByteSource(c.stdin), "NVMe-oF address", defaultAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultAddr {
		t.Errorf("empty input: got %q, want default %q", got, defaultAddr)
	}

	out.Reset()
	c = command(strings.NewReader("10.0.0.2\n"), &out, io.Discard, &options{timeout: time.Second})
	got, err = c.promptString(newByteSource(c.stdin), "NVMe-oF address", defaultAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.2" {
		t.Errorf("override: got %q, want 10.0.0.2", got)
	}
}

func TestPromptTimeoutUsesDefault(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var out bytes.Buffer
	c := command(pr, &out, io.Discard, &options{timeout: 50 * time.Millisecond})
	got, err := c.promptString(newByteSource(pr), "NVMe-oF port", defaultPort)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultPort {
		t.Errorf("timeout: got %q, want default %q", got, defaultPort)
	}
	if !strings.Contains(out.String(), "timeout, using default") {
		t.Errorf("timeout message missing: %q", out.String())
	}
}

func TestPromptKeyStopsTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var out bytes.Buffer
	c := command(pr, &out, io.Discard, &options{timeout: 80 * time.Millisecond})
	src := newByteSource(pr)
	done := make(chan string, 1)
	errc := make(chan error, 1)
	go func() {
		got, err := c.promptString(src, "NVMe-oF address", defaultAddr)
		errc <- err
		done <- got
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := pw.Write([]byte("1")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := pw.Write([]byte("0.0.0.2\n")); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got != "10.0.0.2" {
		t.Errorf("got %q, want 10.0.0.2 after keypress canceled timeout", got)
	}
	if strings.Contains(out.String(), "timeout, using default") {
		t.Errorf("timeout should not fire after a keypress: %q", out.String())
	}
}

func TestPromptUntilYesUsesDefault(t *testing.T) {
	c := command(strings.NewReader(""), io.Discard, io.Discard, &options{yes: true})
	got, err := c.promptUntil(newByteSource(c.stdin), "port", "44420", validatePort)
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
	if o.timeout != defaultPromptTimeout {
		t.Errorf("timeout = %v", o.timeout)
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
