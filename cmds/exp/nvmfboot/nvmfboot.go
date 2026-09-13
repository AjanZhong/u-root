// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

// nvmfboot connects an NVMe-oF target, mounts a partition, and switch_root.
//
// Synopsis:
//
//	nvmfboot [OPTIONS]
//
// Description:
//
//	Intended to run as PID 1 (u-root -initcmd=nvmfboot) so switch_root can
//	replace the ramfs with the remote root filesystem. At each step that
//	needs input, nvmfboot prompts and accepts Enter to keep the default.
//
// Example:
//
//	u-root -initcmd=nvmfboot cmds/exp/nvmfboot cmds/exp/nvme cmds/core/{gosh,dhclient}
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/u-root/u-root/pkg/libinit"
	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/mount/block"
	"golang.org/x/sys/unix"
)

const (
	defaultTransport  = "tcp"
	defaultAddr       = "192.168.2.218"
	defaultPort       = "44420"
	defaultNQN        = "nqn.2026-03.spdk.newfw:3a0b838a-c8fe-4955-a76b-d6767c376f9e"
	defaultHostNQN    = "nqn.2014-08.org.nvmexpress:uuid:035e02d8-04d3-0580-2a06-d40700080009"
	defaultDevice     = "/dev/nvme0n1p4"
	defaultMountPoint = "/mnt"
	defaultInit       = "/sbin/init"
	deviceWait        = 15 * time.Second
)

var (
	errNeedIP        = errors.New("nvmf address must be a valid IP address")
	errNeedTransport = errors.New("transport must be tcp or rdma")
	errNeedPort      = errors.New("port must be an integer between 1 and 65535")
	errNeedValue     = errors.New("value must not be empty")
	connectedRe      = regexp.MustCompile(`connected:\s*(/dev/nvme\d+)`)
)

type options struct {
	transport  string
	traddr     string
	trsvcid    string
	nqn        string
	hostnqn    string
	device     string
	mountPoint string
	init       string
	yes        bool
	deviceSet  bool
}

func (o *options) register(fs *flag.FlagSet) {
	fs.StringVar(&o.transport, "t", defaultTransport, "NVMe-oF transport (tcp or rdma)")
	fs.StringVar(&o.transport, "transport", defaultTransport, "NVMe-oF transport (tcp or rdma)")
	fs.StringVar(&o.traddr, "a", defaultAddr, "NVMe-oF target address")
	fs.StringVar(&o.traddr, "traddr", defaultAddr, "NVMe-oF target address")
	fs.StringVar(&o.trsvcid, "s", defaultPort, "NVMe-oF target port")
	fs.StringVar(&o.trsvcid, "trsvcid", defaultPort, "NVMe-oF target port")
	fs.StringVar(&o.nqn, "n", defaultNQN, "subsystem NQN")
	fs.StringVar(&o.nqn, "nqn", defaultNQN, "subsystem NQN")
	fs.StringVar(&o.hostnqn, "q", defaultHostNQN, "host NQN")
	fs.StringVar(&o.hostnqn, "hostnqn", defaultHostNQN, "host NQN")
	fs.StringVar(&o.device, "d", defaultDevice, "partition to mount")
	fs.StringVar(&o.device, "device", defaultDevice, "partition to mount")
	fs.StringVar(&o.mountPoint, "m", defaultMountPoint, "mount point")
	fs.StringVar(&o.mountPoint, "mountpoint", defaultMountPoint, "mount point")
	fs.StringVar(&o.init, "init", defaultInit, "init to exec after switch_root")
	fs.BoolVar(&o.yes, "y", false, "do not prompt; use flags and defaults")
}

type cmd struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	opts       *options
	exec       func(name string, args ...string) ([]byte, error)
	switchRoot func(newRoot, init string) error
}

func command(stdin io.Reader, stdout, stderr io.Writer, o *options) *cmd {
	c := &cmd{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		opts:       o,
		switchRoot: mount.SwitchRoot,
	}
	c.exec = c.runExec
	return c
}

func (c *cmd) executing(command string) {
	fmt.Fprintf(c.stdout, "nvmfboot: executing: %s\n", command)
}

func (c *cmd) runExec(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(c.stdout, &buf)
	cmd.Stderr = c.stderr
	err := cmd.Run()
	return buf.Bytes(), err
}

func promptString(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	fmt.Fprintf(w, "%s [%s]: ", label, def)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func validateTransport(t string) error {
	switch strings.ToLower(t) {
	case "tcp", "rdma":
		return nil
	default:
		return errNeedTransport
	}
}

func validateAddr(addr string) error {
	if net.ParseIP(addr) == nil {
		return errNeedIP
	}
	return nil
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errNeedPort
	}
	return nil
}

func validateNonEmpty(v string) error {
	if strings.TrimSpace(v) == "" {
		return errNeedValue
	}
	return nil
}

func parseConnectedDevice(output string) (string, error) {
	m := connectedRe.FindStringSubmatch(output)
	if m == nil {
		return "", fmt.Errorf("could not parse nvme device from %q", strings.TrimSpace(output))
	}
	return m[1], nil
}

func (o *options) nvmeConnectArgs() []string {
	return []string{
		"connect",
		"-t", o.transport,
		"-a", o.traddr,
		"-s", o.trsvcid,
		"-n", o.nqn,
		"-hostnqn", o.hostnqn,
	}
}

func defaultPartition(ctrl, fallback string) string {
	base := strings.TrimPrefix(ctrl, "/dev/")
	if !strings.HasPrefix(base, "nvme") {
		return fallback
	}
	return "/dev/" + base + "n1p4"
}

func (c *cmd) promptUntil(r *bufio.Reader, label, def string, validate func(string) error) (string, error) {
	if c.opts.yes {
		if err := validate(def); err != nil {
			return "", err
		}
		return def, nil
	}
	for {
		v, err := promptString(r, c.stdout, label, def)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if err := validate(def); err != nil {
					return "", err
				}
				return def, nil
			}
			return "", err
		}
		if err := validate(v); err != nil {
			fmt.Fprintf(c.stdout, "invalid: %v\n", err)
			continue
		}
		return v, nil
	}
}

func ensureRunTmpfs() error {
	if err := os.MkdirAll("/run", 0o755); err != nil {
		return err
	}
	same, err := mount.SameFilesystem("/", "/run")
	if err == nil && !same {
		log.Printf("/run already mounted")
		return nil
	}
	_, err = mount.Mount("tmpfs", "/run", "tmpfs", "", 0)
	if err != nil && !errors.Is(err, unix.EBUSY) && !errors.Is(err, unix.EEXIST) {
		return err
	}
	return nil
}

func waitForPath(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func listNVMeBlockDevs(ctrl string) []string {
	prefix := filepath.Base(ctrl)
	devs, err := block.GetBlockDevices()
	if err != nil {
		matches, globErr := filepath.Glob("/dev/" + prefix + "*")
		if globErr != nil {
			return nil
		}
		return matches
	}
	var out []string
	for _, d := range devs {
		if d.Name == prefix || strings.HasPrefix(d.Name, prefix+"n") {
			out = append(out, d.DevicePath())
		}
	}
	return out
}

func rescanPartitions(ctrl string) {
	ns := ctrl + "n1"
	if err := waitForPath(ns, deviceWait); err != nil {
		log.Printf("waiting for namespace %s: %v", ns, err)
		return
	}
	d, err := block.Device(ns)
	if err != nil {
		log.Printf("lookup %s: %v", ns, err)
		return
	}
	if err := d.ReadPartitionTable(); err != nil {
		log.Printf("reread partition table on %s: %v", ns, err)
	}
}

func maybeInitSetup() {
	if os.Getpid() != 1 {
		return
	}
	libinit.SetEnv()
	libinit.CreateRootfs()
	libinit.NetInit()
	if err := libinit.InstallAllModules(); err != nil {
		log.Printf("module load: %v", err)
	}
}

func emergencyShell() {
	log.Printf("dropping to emergency shell")
	for {
		for _, sh := range []string{"/bbin/gosh", "/bin/defaultsh", "/bin/sh"} {
			cmd := exec.Command(sh)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				log.Printf("%s: %v", sh, err)
			}
		}
	}
}

func (c *cmd) run() error {
	maybeInitSetup()

	if pid := os.Getpid(); pid != 1 {
		log.Printf("warning: pid is %d, not 1; switch_root needs pid 1 (use u-root -initcmd=nvmfboot)", pid)
	}

	c.executing("mkdir -p /run")
	c.executing("mount -t tmpfs tmpfs /run")
	if err := ensureRunTmpfs(); err != nil {
		return fmt.Errorf("mount tmpfs on /run: %w", err)
	}

	c.executing("dhclient -ipv6=false")
	if _, err := c.exec("dhclient", "-ipv6=false"); err != nil {
		return fmt.Errorf("dhclient: %w", err)
	}

	r := bufio.NewReader(c.stdin)
	fmt.Fprintln(c.stdout, "nvmfboot: NVMe-oF connect parameters (Enter keeps the default)")
	transport, err := c.promptUntil(r, "NVMe-oF transport (tcp or rdma)", c.opts.transport, validateTransport)
	if err != nil {
		return err
	}
	c.opts.transport = strings.ToLower(transport)

	addr, err := c.promptUntil(r, "NVMe-oF address", c.opts.traddr, validateAddr)
	if err != nil {
		return err
	}
	c.opts.traddr = addr

	port, err := c.promptUntil(r, "NVMe-oF port", c.opts.trsvcid, validatePort)
	if err != nil {
		return err
	}
	c.opts.trsvcid = port

	nqn, err := c.promptUntil(r, "Subsystem NQN", c.opts.nqn, validateNonEmpty)
	if err != nil {
		return err
	}
	c.opts.nqn = nqn

	hostnqn, err := c.promptUntil(r, "Host NQN", c.opts.hostnqn, validateNonEmpty)
	if err != nil {
		return err
	}
	c.opts.hostnqn = hostnqn

	nvmeArgs := c.opts.nvmeConnectArgs()
	c.executing("nvme " + strings.Join(nvmeArgs, " "))
	out, err := c.exec("nvme", nvmeArgs...)
	if err != nil {
		return fmt.Errorf("nvme connect: %w", err)
	}
	ctrl, err := parseConnectedDevice(string(out))
	if err != nil {
		fmt.Fprintf(c.stdout, "nvmfboot: nvme connect output:\n%s\n", string(out))
		return err
	}
	fmt.Fprintf(c.stdout, "nvmfboot: connected nvme device is %s\n", ctrl)

	rescanPartitions(ctrl)
	if !c.opts.deviceSet {
		c.opts.device = defaultPartition(ctrl, c.opts.device)
	}
	if devs := listNVMeBlockDevs(ctrl); len(devs) > 0 {
		fmt.Fprintf(c.stdout, "nvmfboot: available NVMe devices: %s\n", strings.Join(devs, " "))
	}

	fmt.Fprintln(c.stdout, "nvmfboot: mount parameters (Enter keeps the default)")
	device, err := c.promptUntil(r, "Mount device partition", c.opts.device, validateNonEmpty)
	if err != nil {
		return err
	}
	c.opts.device = device

	mp, err := c.promptUntil(r, "Mount point", c.opts.mountPoint, validateNonEmpty)
	if err != nil {
		return err
	}
	c.opts.mountPoint = mp

	if err := waitForPath(c.opts.device, deviceWait); err != nil {
		return err
	}
	c.executing("mkdir -p " + c.opts.mountPoint)
	if err := os.MkdirAll(c.opts.mountPoint, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", c.opts.mountPoint, err)
	}
	c.executing("mount " + c.opts.device + " " + c.opts.mountPoint)
	if _, err := mount.TryMount(c.opts.device, c.opts.mountPoint, "", 0); err != nil {
		return fmt.Errorf("mount %s on %s: %w", c.opts.device, c.opts.mountPoint, err)
	}

	c.executing("switch_root " + c.opts.mountPoint + " " + c.opts.init)
	if err := c.switchRoot(c.opts.mountPoint, c.opts.init); err != nil {
		return fmt.Errorf("switch_root: %w", err)
	}
	return nil
}

func main() {
	log.SetPrefix("nvmfboot: ")
	log.SetFlags(0)

	o := &options{}
	o.register(flag.CommandLine)
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "d" || f.Name == "device" {
			o.deviceSet = true
		}
	})

	c := command(os.Stdin, os.Stdout, os.Stderr, o)
	if err := c.run(); err != nil {
		log.Print(err)
		if os.Getpid() == 1 {
			emergencyShell()
		}
		os.Exit(1)
	}
}
