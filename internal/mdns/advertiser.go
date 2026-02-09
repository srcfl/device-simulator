package mdns

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// lookupFunc defines the signature for network hostname collision checks.
// Returns true if the hostname is already in use on the network.
type lookupFunc func(hostname string) bool

// Advertiser manages mDNS hostname advertisements for simulators.
// Each simulator can advertise a hostname on the local network so that
// tools like the Zap gateway can discover simulated devices via mDNS.
type Advertiser struct {
	mu       sync.Mutex
	servers  map[int]*mdns.Server // simulator ID -> mDNS server
	hosts    map[string]int       // hostname -> simulator ID (collision tracking)
	lookup   lookupFunc           // network collision check (injectable for testing)
}

// New creates a new Advertiser with network-level collision detection.
func New() *Advertiser {
	return &Advertiser{
		servers: make(map[int]*mdns.Server),
		hosts:   make(map[string]int),
		lookup:  networkLookup,
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

	// Resolve a unique hostname (handles collisions with real devices and other simulators)
	resolvedHost := a.resolveHostname(hostname)

	// Get local IP for the host record
	ip := localIP()

	// Set up the mDNS service
	if serviceType == "" {
		serviceType = "_device._tcp"
	}

	service, err := mdns.NewMDNSService(
		resolvedHost,           // instance name
		serviceType,            // service type
		"",                     // domain (default .local)
		resolvedHost+".local.", // host name
		port,                   // port
		[]net.IP{ip},           // IPs
		txt,                    // TXT records
	)
	if err != nil {
		return fmt.Errorf("mdns service setup: %w", err)
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return fmt.Errorf("mdns server start: %w", err)
	}

	a.servers[simID] = server
	a.hosts[resolvedHost] = simID

	log.Printf("[mDNS] Advertising %s.local for simulator %d (port %d, service %s)", resolvedHost, simID, port, serviceType)
	return nil
}

// Remove stops the mDNS advertisement for a simulator.
func (a *Advertiser) Remove(simID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removeLocked(simID)
}

func (a *Advertiser) removeLocked(simID int) {
	server, ok := a.servers[simID]
	if !ok {
		return
	}

	// Find and remove the hostname entry
	for host, id := range a.hosts {
		if id == simID {
			log.Printf("[mDNS] Stopped advertising %s.local for simulator %d", host, simID)
			delete(a.hosts, host)
			break
		}
	}

	server.Shutdown()
	delete(a.servers, simID)
}

// Shutdown stops all mDNS advertisements.
func (a *Advertiser) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, server := range a.servers {
		server.Shutdown()
		delete(a.servers, id)
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
		// Check internal collision first (fast, no network)
		if _, taken := a.hosts[candidate]; taken {
			candidate = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		// Check network collision (slow, involves mDNS query)
		if a.lookup != nil && a.lookup(candidate) {
			log.Printf("[mDNS] Hostname %s.local already in use on network, trying next", candidate)
			candidate = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		return candidate
	}
}

// networkLookup checks if a hostname is already advertised on the local network
// by performing a DNS lookup for hostname.local with a short timeout.
func networkLookup(hostname string) bool {
	// Use a custom resolver with a short timeout to avoid blocking startup
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
