// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

// nvme discovers and connects NVMe over Fabrics (NVMe-oF) targets.
//
// Synopsis:
//
//	nvme discover -t tcp -a <traddr> [-s <trsvcid>]
//	nvme connect  -t tcp -a <traddr> -n <nqn> [-s <trsvcid>]
//	nvme disconnect -n <nqn> | -d <device>
//
// Description:
//
//	A small nvme-cli compatible helper that talks to /dev/nvme-fabrics.
//	discover connects to a discovery controller, prints the discovery log,
//	and disconnects unless -p is set. connect creates an I/O controller.
//	The kernel needs nvme-fabrics plus a transport such as nvme-tcp.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const discoveryNQN = "nqn.2014-08.org.nvmexpress.discovery"

var (
	errUsage             = errors.New("usage: nvme <discover|connect|disconnect> [options]")
	errNeedTransport     = errors.New("need a transport (-t)")
	errNeedAddress       = errors.New("need a transport address (-a)")
	errNeedNQN           = errors.New("need a subsystem NQN (-n)")
	errNeedDisconnectArg = errors.New("need a subsystem NQN (-n) or device (-d)")
)

type options struct {
	transport      string
	traddr         string
	trsvcid        string
	nqn            string
	hostnqn        string
	hostid         string
	hostTraddr     string
	hostIface      string
	nrIOQueues     int
	nrWriteQueues  int
	nrPollQueues   int
	queueSize      int
	keepAliveTMO   int
	setKeepAlive   bool
	reconnectDelay int
	ctrlLossTMO    int
	setCtrlLoss    bool
	tos            int
	setTOS         bool
	duplicate      bool
	hdrDigest      bool
	dataDigest     bool
	persistent     bool
	device         string
	verbose        bool
}

func (o *options) register(fs *flag.FlagSet) {
	fs.StringVar(&o.transport, "t", "", "transport type (tcp, rdma, fc, loop)")
	fs.StringVar(&o.transport, "transport", "", "transport type (tcp, rdma, fc, loop)")
	fs.StringVar(&o.traddr, "a", "", "transport address")
	fs.StringVar(&o.traddr, "traddr", "", "transport address")
	fs.StringVar(&o.trsvcid, "s", "", "transport service id (port)")
	fs.StringVar(&o.trsvcid, "trsvcid", "", "transport service id (port)")
	fs.StringVar(&o.nqn, "n", "", "subsystem NQN")
	fs.StringVar(&o.nqn, "nqn", "", "subsystem NQN")
	fs.StringVar(&o.hostnqn, "q", "", "host NQN")
	fs.StringVar(&o.hostnqn, "hostnqn", "", "host NQN")
	fs.StringVar(&o.hostid, "I", "", "host ID (UUID)")
	fs.StringVar(&o.hostid, "hostid", "", "host ID (UUID)")
	fs.StringVar(&o.hostTraddr, "w", "", "host transport address")
	fs.StringVar(&o.hostTraddr, "host-traddr", "", "host transport address")
	fs.StringVar(&o.hostIface, "f", "", "host network interface")
	fs.StringVar(&o.hostIface, "host-iface", "", "host network interface")
	fs.IntVar(&o.nrIOQueues, "i", 0, "number of I/O queues")
	fs.IntVar(&o.nrIOQueues, "nr-io-queues", 0, "number of I/O queues")
	fs.IntVar(&o.nrWriteQueues, "W", 0, "number of write queues")
	fs.IntVar(&o.nrWriteQueues, "nr-write-queues", 0, "number of write queues")
	fs.IntVar(&o.nrPollQueues, "P", 0, "number of poll queues")
	fs.IntVar(&o.nrPollQueues, "nr-poll-queues", 0, "number of poll queues")
	fs.IntVar(&o.queueSize, "Q", 0, "queue size")
	fs.IntVar(&o.queueSize, "queue-size", 0, "queue size")
	fs.IntVar(&o.keepAliveTMO, "k", 0, "keep-alive timeout in seconds")
	fs.IntVar(&o.keepAliveTMO, "keep-alive-tmo", 0, "keep-alive timeout in seconds")
	fs.IntVar(&o.reconnectDelay, "c", 0, "reconnect delay in seconds")
	fs.IntVar(&o.reconnectDelay, "reconnect-delay", 0, "reconnect delay in seconds")
	fs.IntVar(&o.ctrlLossTMO, "l", 0, "controller loss timeout in seconds")
	fs.IntVar(&o.ctrlLossTMO, "ctrl-loss-tmo", 0, "controller loss timeout in seconds")
	fs.IntVar(&o.tos, "T", -1, "type of service")
	fs.IntVar(&o.tos, "tos", -1, "type of service")
	fs.BoolVar(&o.duplicate, "D", false, "allow duplicate connections")
	fs.BoolVar(&o.duplicate, "duplicate-connect", false, "allow duplicate connections")
	fs.BoolVar(&o.hdrDigest, "g", false, "enable header digest")
	fs.BoolVar(&o.hdrDigest, "hdr-digest", false, "enable header digest")
	fs.BoolVar(&o.dataDigest, "G", false, "enable data digest")
	fs.BoolVar(&o.dataDigest, "data-digest", false, "enable data digest")
	fs.BoolVar(&o.persistent, "p", false, "keep discovery controller connected")
	fs.BoolVar(&o.persistent, "persistent", false, "keep discovery controller connected")
	fs.StringVar(&o.device, "d", "", "nvme device (e.g. nvme0 or /dev/nvme0)")
	fs.StringVar(&o.device, "device", "", "nvme device (e.g. nvme0 or /dev/nvme0)")
	fs.BoolVar(&o.verbose, "v", false, "verbose output")
}

func parseOptions(name string, args []string) (*options, error) {
	o := &options{tos: -1}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	o.register(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// Detect whether keep-alive / ctrl-loss were explicitly passed.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "k", "keep-alive-tmo":
			o.setKeepAlive = true
		case "l", "ctrl-loss-tmo":
			o.setCtrlLoss = true
		case "T", "tos":
			o.setTOS = true
		}
	})
	return o, nil
}

func (o *options) prepare(discover bool) error {
	if o.transport == "" {
		return errNeedTransport
	}
	if o.transport != "loop" && o.traddr == "" {
		return errNeedAddress
	}
	if discover {
		if o.nqn == "" {
			o.nqn = discoveryNQN
		}
		if !o.persistent && !o.setKeepAlive {
			o.keepAliveTMO = 0
			o.setKeepAlive = true
		}
		if o.persistent && !o.setKeepAlive {
			o.keepAliveTMO = 120
			o.setKeepAlive = true
		}
	} else if o.nqn == "" {
		return errNeedNQN
	}
	if o.trsvcid == "" {
		o.trsvcid = defaultTrsvcid(o.transport, discover)
	}
	if err := o.fillHostIdentity(); err != nil {
		return err
	}
	if o.transport == "tcp" || o.transport == "rdma" {
		addr, err := resolveTraddr(o.traddr)
		if err != nil {
			return err
		}
		o.traddr = addr
	}
	return nil
}

func run(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errUsage
	}
	switch args[0] {
	case "discover":
		return cmdDiscover(args[1:], stdout)
	case "connect":
		return cmdConnect(args[1:], stdout)
	case "disconnect":
		return cmdDisconnect(args[1:], stdout)
	default:
		return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
}

func cmdDiscover(args []string, stdout io.Writer) error {
	o, err := parseOptions("nvme discover", args)
	if err != nil {
		return err
	}
	if err := o.prepare(true); err != nil {
		return err
	}
	argstr := o.connectString(true)
	if o.verbose {
		fmt.Fprintf(os.Stderr, "connect string: %s\n", argstr)
	}
	instance, err := addCtrl(argstr)
	if err != nil {
		return err
	}
	if !o.persistent {
		defer func() {
			if derr := deleteCtrl(instance); derr != nil {
				log.Printf("disconnect nvme%d: %v", instance, derr)
			}
		}()
	} else {
		fmt.Fprintf(stdout, "Persistent device: nvme%d\n", instance)
	}

	logPage, err := getDiscoveryLog(instance)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, logPage.String())
	return nil
}

func cmdConnect(args []string, stdout io.Writer) error {
	o, err := parseOptions("nvme connect", args)
	if err != nil {
		return err
	}
	if err := o.prepare(false); err != nil {
		return err
	}
	argstr := o.connectString(false)
	if o.verbose {
		fmt.Fprintf(os.Stderr, "connect string: %s\n", argstr)
	}
	instance, err := addCtrl(argstr)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "connected: /dev/nvme%d\n", instance)
	return nil
}

func cmdDisconnect(args []string, stdout io.Writer) error {
	o, err := parseOptions("nvme disconnect", args)
	if err != nil {
		return err
	}
	if o.device == "" && o.nqn == "" {
		return errNeedDisconnectArg
	}
	var instances []int
	if o.device != "" {
		inst, err := parseDeviceInstance(o.device)
		if err != nil {
			return err
		}
		instances = []int{inst}
	} else {
		instances, err = instancesByNQN(o.nqn)
		if err != nil {
			return err
		}
		if len(instances) == 0 {
			return fmt.Errorf("no controller with nqn %q", o.nqn)
		}
	}
	for _, inst := range instances {
		if err := deleteCtrl(inst); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "disconnected: nvme%d\n", inst)
	}
	return nil
}

func parseDeviceInstance(dev string) (int, error) {
	dev = strings.TrimPrefix(dev, "/dev/")
	var n int
	if _, err := fmt.Sscanf(dev, "nvme%d", &n); err != nil {
		return 0, fmt.Errorf("invalid nvme device %q", dev)
	}
	return n, nil
}

func main() {
	log.SetPrefix("nvme: ")
	log.SetFlags(0)
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, errUsage) {
			log.Print(errUsage)
			os.Exit(2)
		}
		log.Fatal(err)
	}
}
