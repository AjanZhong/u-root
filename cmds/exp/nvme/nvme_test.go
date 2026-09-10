// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unsafe"
)

func TestConnectString(t *testing.T) {
	o := &options{
		nqn:          "nqn.2014-08.example:nvme:disk1",
		transport:    "tcp",
		traddr:       "192.0.2.10",
		trsvcid:      "4420",
		hostnqn:      "nqn.2014-08.org.nvmexpress:uuid:11111111-2222-3333-4444-555555555555",
		hostid:       "11111111-2222-3333-4444-555555555555",
		nrIOQueues:   4,
		queueSize:    128,
		setKeepAlive: true,
		keepAliveTMO: 0,
		hdrDigest:    true,
	}
	got := o.connectString(false)
	for _, want := range []string{
		"nqn=nqn.2014-08.example:nvme:disk1",
		"transport=tcp",
		"traddr=192.0.2.10",
		"trsvcid=4420",
		"hostnqn=nqn.2014-08.org.nvmexpress:uuid:11111111-2222-3333-4444-555555555555",
		"nr_io_queues=4",
		"queue_size=128",
		"keep_alive_tmo=0",
		"hdr_digest",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("connectString() = %q, want substring %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "nqn=") {
		t.Errorf("nqn should be first: %q", got)
	}

	disc := o.connectString(true)
	if strings.Contains(disc, "nr_io_queues") || strings.Contains(disc, "queue_size") {
		t.Errorf("discover connect string should omit I/O queue options: %q", disc)
	}
}

func TestParseInstance(t *testing.T) {
	n, err := parseInstance("instance=5,cntlid=1\n")
	if err != nil || n != 5 {
		t.Fatalf("parseInstance(...) = %d, %v, want 5, nil", n, err)
	}
	if _, err := parseInstance("cntlid=1\n"); err == nil {
		t.Fatal("parseInstance(missing instance) = nil, want error")
	}
}

func TestParseDeviceInstance(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"nvme0", 0, false},
		{"/dev/nvme3", 3, false},
		{"nvme0n1", 0, false},
		{"/dev/nvme12n2", 12, false},
		{"sda", 0, true},
	}
	for _, tt := range tests {
		got, err := parseDeviceInstance(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDeviceInstance(%q) = %d, nil, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseDeviceInstance(%q) = %d, %v, want %d, nil", tt.in, got, err, tt.want)
		}
	}
}

func TestDefaultTrsvcid(t *testing.T) {
	if got := defaultTrsvcid("tcp", true); got != "8009" {
		t.Errorf("tcp discover port = %q, want 8009", got)
	}
	if got := defaultTrsvcid("tcp", false); got != "4420" {
		t.Errorf("tcp connect port = %q, want 4420", got)
	}
	if got := defaultTrsvcid("rdma", false); got != "4420" {
		t.Errorf("rdma port = %q, want 4420", got)
	}
	if got := defaultTrsvcid("fc", false); got != "" {
		t.Errorf("fc port = %q, want empty", got)
	}
}

func TestCString(t *testing.T) {
	b := make([]byte, 8)
	copy(b, "4420")
	if got := cString(b); got != "4420" {
		t.Errorf("cString = %q, want 4420", got)
	}
	padded := []byte{' ', 't', 'c', 'p', ' ', 0, 0}
	if got := cString(padded); got != "tcp" {
		t.Errorf("cString padded = %q, want tcp", got)
	}
}

func TestParseDiscoveryLog(t *testing.T) {
	buf := make([]byte, discHeaderSize+discEntrySize)
	binary.LittleEndian.PutUint64(buf[0:8], 7)
	binary.LittleEndian.PutUint64(buf[8:16], 1)
	binary.LittleEndian.PutUint16(buf[16:18], 0)

	e := buf[discHeaderSize:]
	e[0] = 3 // tcp
	e[1] = 1 // ipv4
	e[2] = 2 // nvme subsystem
	e[3] = 0
	binary.LittleEndian.PutUint16(e[4:6], 1)
	binary.LittleEndian.PutUint16(e[6:8], 0xffff)
	binary.LittleEndian.PutUint16(e[8:10], 32)
	copy(e[32:64], "4420")
	copy(e[256:512], "nqn.2014-08.example:nvme:disk1")
	copy(e[512:768], "192.0.2.10")
	e[768] = 0

	log, err := parseDiscoveryLog(buf)
	if err != nil {
		t.Fatal(err)
	}
	if log.GenCtr != 7 || log.NumRec != 1 || len(log.Entries) != 1 {
		t.Fatalf("header = gen %d num %d entries %d", log.GenCtr, log.NumRec, len(log.Entries))
	}
	ent := log.Entries[0]
	if ent.Trsvcid != "4420" || ent.Traddr != "192.0.2.10" || ent.Subnqn != "nqn.2014-08.example:nvme:disk1" {
		t.Errorf("entry = %+v", ent)
	}
	out := log.String()
	for _, want := range []string{"tcp", "ipv4", "nvme subsystem", "192.0.2.10", "4420"} {
		if !strings.Contains(out, want) {
			t.Errorf("String() missing %q:\n%s", want, out)
		}
	}
}

func TestParseDiscoveryHeaderOnly(t *testing.T) {
	buf := make([]byte, discHeaderSize)
	binary.LittleEndian.PutUint64(buf[8:16], 3)
	_, numrec, _, err := parseDiscoveryHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if numrec != 3 {
		t.Fatalf("numrec = %d, want 3", numrec)
	}
	if _, err := parseDiscoveryLog(buf); err == nil {
		t.Fatal("parseDiscoveryLog(header with numrec=3) = nil, want truncated error")
	}
}

func TestPrepareValidation(t *testing.T) {
	o := &options{transport: "tcp"}
	if err := o.prepare(false); !errors.Is(err, errNeedAddress) {
		t.Errorf("prepare connect missing addr = %v, want %v", err, errNeedAddress)
	}
	o.traddr = "127.0.0.1"
	if err := o.prepare(false); !errors.Is(err, errNeedNQN) {
		t.Errorf("prepare connect missing nqn = %v, want %v", err, errNeedNQN)
	}
	o2 := &options{traddr: "127.0.0.1"}
	if err := o2.prepare(true); !errors.Is(err, errNeedTransport) {
		t.Errorf("prepare discover missing transport = %v, want %v", err, errNeedTransport)
	}
}

func TestPrepareDiscoverDefaults(t *testing.T) {
	o := &options{transport: "tcp", traddr: "192.0.2.1"}
	if err := o.prepare(true); err != nil {
		t.Fatal(err)
	}
	if o.nqn != discoveryNQN {
		t.Errorf("nqn = %q, want discovery NQN", o.nqn)
	}
	if o.trsvcid != "8009" {
		t.Errorf("trsvcid = %q, want 8009", o.trsvcid)
	}
	if !o.setKeepAlive || o.keepAliveTMO != 0 {
		t.Errorf("discover keep-alive = %d set=%v, want 0 true", o.keepAliveTMO, o.setKeepAlive)
	}
	if o.hostnqn == "" || o.hostid == "" {
		t.Fatal("expected generated host identity")
	}
}

func TestRunUsage(t *testing.T) {
	var buf bytes.Buffer
	if err := run(nil, &buf); !errors.Is(err, errUsage) {
		t.Errorf("run(nil) = %v, want %v", err, errUsage)
	}
	if err := run([]string{"frob"}, &buf); !errors.Is(err, errUsage) {
		t.Errorf("run(frob) = %v, want %v", err, errUsage)
	}
	if err := run([]string{"connect", "-t", "tcp", "-a", "192.0.2.1"}, &buf); !errors.Is(err, errNeedNQN) {
		t.Errorf("run connect missing nqn = %v, want %v", err, errNeedNQN)
	}
	if err := run([]string{"disconnect"}, &buf); !errors.Is(err, errNeedDisconnectArg) {
		t.Errorf("run disconnect = %v, want %v", err, errNeedDisconnectArg)
	}
}

func TestIoctlLayout(t *testing.T) {
	if sz := unsafe.Sizeof(nvmePassthruCmd{}); sz != 72 {
		t.Fatalf("nvmePassthruCmd size = %d, want 72", sz)
	}
	const want = 0xC0484E41
	if nvmeIoctlAdminCmd != want {
		t.Fatalf("nvmeIoctlAdminCmd = 0x%x, want 0x%x", nvmeIoctlAdminCmd, want)
	}
}
