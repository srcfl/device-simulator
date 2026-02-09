package mdns

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// registration tracks a running mDNS registration process.
type registration struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// lookupFunc defines the signature for network hostname collision checks.
type lookupFunc func(hostname string) bool

// Advertiser manages mDNS hostname advertisements for simulators.
// Uses the system's native mDNS daemon (dns-sd on macOS, avahi on Linux)
// for reliable hostname resolution on the local network.
type Advertiser struct {
	mu     sync.Mutex
	regs   map[int]*registration // simulator ID -> running process
	hosts  map[string]int        // hostname -> simulator ID (collision tracking)
	lookup lookupFunc            // network collision check (injectable for testing)
}

// New creates a new Advertiser.
func New() *Advertiser {
	return &Advertiser{
		regs:   make(map[int]*registration),
		hosts:  make(map[string]int),
		lookup: networkLookup,
	}
}

// Advertise starts advertising a hostname via mDNS for the given simulator.
// If the hostname is already in use (internally or on the network), it
// appends -1, -2, etc. until an unused name is found.
// Returns nil on success. A no-op if hostname is empty.
func (a *Advertiser) Advertise(simID int, hostname string, port int, serviceType string, txt []string) error {
	if hostname == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Remove existing advertisement for this simulator if any
	a.removeLocked(simID)

	// Resolve a unique hostname
	resolvedHost := a.resolveHostname(hostname)

	if serviceType == "" {
		serviceType = "_device._tcp"
	}

	ip := localIP()

	reg, err := startRegistrationVar(resolvedHost, serviceType, port, ip.String())
	if err != nil {
		return fmt.Errorf("mdns registration: %w", err)
	}

	a.regs[simID] = reg
	a.hosts[resolvedHost] = simID

	log.Printf("[mDNS] Advertising %s.local for simulator %d (port %d, service %s, ip %s)", resolvedHost, simID, port, serviceType, ip)
	return nil
}

// Remove stops the mDNS advertisement for a simulator.
func (a *Advertiser) Remove(simID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removeLocked(simID)
}

func (a *Advertiser) removeLocked(simID int) {
	reg, ok := a.regs[simID]
	if !ok {
		return
	}

	for host, id := range a.hosts {
		if id == simID {
			log.Printf("[mDNS] Stopped advertising %s.local for simulator %d", host, simID)
			delete(a.hosts, host)
			break
		}
	}

	reg.cancel()
	delete(a.regs, simID)
}

// Shutdown stops all mDNS advertisements.
func (a *Advertiser) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, reg := range a.regs {
		reg.cancel()
		delete(a.regs, id)
	}
	for host := range a.hosts {
		delete(a.hosts, host)
	}
	log.Printf("[mDNS] All advertisements stopped")
}

// ActiveHostname returns the hostname being advertised for a simulator,
// or empty string if none.
func (a *Advertiser) ActiveHostname(simID int) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	for host, id := range a.hosts {
		if id == simID {
			return host
		}
	}
	return ""
}

// resolveHostname finds a unique hostname by checking for collisions both
// internally (other simulators) and on the network (real devices).
// Must be called with a.mu held.
func (a *Advertiser) resolveHostname(base string) string {
	candidate := base

	for i := 1; ; i++ {
		if _, taken := a.hosts[candidate]; taken {
			candidate = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		if a.lookup != nil && a.lookup(candidate) {
			log.Printf("[mDNS] Hostname %s.local already in use on network, trying next", candidate)
			candidate = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		return candidate
	}
}

// startRegistrationVar is the function used to start a registration.
// Tests can replace this to avoid launching real system processes.
var startRegistrationVar = startRegistration

// startRegistration launches a system mDNS registration process.
// On macOS: dns-sd -P (registers both service and hostname through mDNSResponder)
// On Linux: avahi-publish-service (registers through avahi-daemon)
func startRegistration(hostname, serviceType string, port int, ip string) (*registration, error) {
	ctx, cancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// dns-sd -P <Name> <Type> <Domain> <Port> <Host> <IP>
		// Registers a proxy service with explicit hostname, which makes
		// both the service and the A record resolvable via mDNSResponder.
		cmd = exec.CommandContext(ctx, "dns-sd", "-P",
			hostname,            // instance name
			serviceType,         // service type (e.g. _mqtt._tcp)
			"local",             // domain
			fmt.Sprintf("%d", port),
			hostname+".local.",  // target host
			ip,                  // IP address
		)
	case "linux":
		// avahi-publish-service <name> <type> <port> [TXT ...]
		// Falls back if avahi-publish is not installed.
		cmd = exec.CommandContext(ctx, "avahi-publish-service",
			hostname,
			serviceType+".local",
			fmt.Sprintf("%d", port),
		)
	default:
		cancel()
		return nil, fmt.Errorf("mDNS not supported on %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start mDNS registration: %w", err)
	}

	// Log if the process exits unexpectedly
	go func() {
		if err := cmd.Wait(); err != nil {
			// Only log if not cancelled (context.Canceled means we stopped it intentionally)
			if ctx.Err() == nil {
				log.Printf("[mDNS] Registration process for %s exited: %v", hostname, err)
			}
		}
	}()

	return &registration{cmd: cmd, cancel: cancel}, nil
}

// networkLookup checks if a hostname is already advertised on the local network.
func networkLookup(hostname string) bool {
	resolver := &net.Resolver{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	addrs, err := resolver.LookupHost(ctx, hostname+".local")
	return err == nil && len(addrs) > 0
}

// localIP returns the first non-loopback IPv4 address found on the system.
func localIP() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return net.IPv4(127, 0, 0, 1)
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP
			}
		}
	}

	return net.IPv4(127, 0, 0, 1)
}
