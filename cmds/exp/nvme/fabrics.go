// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultFabricsPath = "/dev/nvme-fabrics"
	hostNQNFile        = "/etc/nvme/hostnqn"
	hostIDFile         = "/etc/nvme/hostid"
	sysfsNVMeDir       = "/sys/class/nvme"
)

var (
	fabricsPath = defaultFabricsPath
	hostNQNPath = hostNQNFile
	hostIDPath  = hostIDFile
	nvmeSysfs   = sysfsNVMeDir
	devWait     = 5 * time.Second
)

func defaultTrsvcid(transport string, discover bool) string {
	switch transport {
	case "tcp":
		if discover {
			return "8009"
		}
		return "4420"
	case "rdma":
		return "4420"
	default:
		return ""
	}
}

func resolveTraddr(traddr string) (string, error) {
	if traddr == "" {
		return "", nil
	}
	if ip := net.ParseIP(traddr); ip != nil {
		return traddr, nil
	}
	ips, err := net.LookupIP(traddr)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", traddr, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("resolve %s: no addresses", traddr)
	}
	return ips[0].String(), nil
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (o *options) fillHostIdentity() error {
	if o.hostnqn == "" {
		o.hostnqn = readTrim(hostNQNPath)
	}
	if o.hostid == "" {
		o.hostid = readTrim(hostIDPath)
	}
	if o.hostid == "" {
		o.hostid = uuid.New().String()
	}
	if o.hostnqn == "" {
		o.hostnqn = "nqn.2014-08.org.nvmexpress:uuid:" + o.hostid
	}
	return nil
}

func addKV(b *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	b.WriteByte(',')
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(val)
}

func addInt(b *strings.Builder, key string, val int, always bool) {
	if !always && val == 0 {
		return
	}
	fmt.Fprintf(b, ",%s=%d", key, val)
}

func addFlag(b *strings.Builder, key string, on bool) {
	if !on {
		return
	}
	b.WriteByte(',')
	b.WriteString(key)
}

func (o *options) connectString(discover bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nqn=%s", o.nqn)
	addKV(&b, "transport", o.transport)
	addKV(&b, "traddr", o.traddr)
	addKV(&b, "trsvcid", o.trsvcid)
	addKV(&b, "hostnqn", o.hostnqn)
	addKV(&b, "hostid", o.hostid)
	addKV(&b, "host_traddr", o.hostTraddr)
	addKV(&b, "host_iface", o.hostIface)
	if !discover {
		addInt(&b, "nr_io_queues", o.nrIOQueues, false)
		addInt(&b, "queue_size", o.queueSize, false)
	}
	addInt(&b, "nr_write_queues", o.nrWriteQueues, false)
	addInt(&b, "nr_poll_queues", o.nrPollQueues, false)
	addInt(&b, "keep_alive_tmo", o.keepAliveTMO, o.setKeepAlive)
	addInt(&b, "reconnect_delay", o.reconnectDelay, false)
	if o.transport != "loop" {
		addInt(&b, "ctrl_loss_tmo", o.ctrlLossTMO, o.setCtrlLoss)
	}
	if o.setTOS {
		addInt(&b, "tos", o.tos, true)
	}
	addFlag(&b, "duplicate_connect", o.duplicate)
	addFlag(&b, "hdr_digest", o.hdrDigest)
	addFlag(&b, "data_digest", o.dataDigest)
	return b.String()
}

func parseInstance(s string) (int, error) {
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		key, val, ok := strings.Cut(field, "=")
		if !ok || key != "instance" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return 0, fmt.Errorf("parse instance %q: %w", val, err)
		}
		return n, nil
	}
	return 0, fmt.Errorf("no instance in fabrics response %q", strings.TrimSpace(s))
}

func addCtrl(argstr string) (int, error) {
	f, err := os.OpenFile(fabricsPath, os.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w (load nvme-fabrics and a transport such as nvme-tcp)", fabricsPath, err)
	}
	defer f.Close()

	if _, err := f.Write([]byte(argstr)); err != nil {
		return -1, fmt.Errorf("write %s: %w", fabricsPath, err)
	}
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil {
		return -1, fmt.Errorf("read %s: %w", fabricsPath, err)
	}
	instance, err := parseInstance(string(buf[:n]))
	if err != nil {
		return -1, err
	}
	if err := waitDev(fmt.Sprintf("/dev/nvme%d", instance), devWait); err != nil {
		_ = deleteCtrl(instance)
		return -1, err
	}
	return instance, nil
}

func waitDev(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
			f.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func deleteCtrl(instance int) error {
	p := filepath.Join(nvmeSysfs, fmt.Sprintf("nvme%d", instance), "delete_controller")
	if err := os.WriteFile(p, []byte("1"), 0o200); err != nil {
		return fmt.Errorf("delete nvme%d: %w", instance, err)
	}
	return nil
}

func instancesByNQN(nqn string) ([]int, error) {
	ents, err := os.ReadDir(nvmeSysfs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", nvmeSysfs, err)
	}
	var out []int
	for _, e := range ents {
		got := readTrim(filepath.Join(nvmeSysfs, e.Name(), "subsysnqn"))
		if got != nqn {
			continue
		}
		inst, err := parseDeviceInstance(e.Name())
		if err != nil {
			continue
		}
		out = append(out, inst)
	}
	return out, nil
}
