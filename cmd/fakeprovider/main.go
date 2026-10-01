// Command fakeprovider runs local fake WireGuard VPN servers for trying lanepool
// without a VPN account. Paste the printed configs into the dashboard under
// Providers → WireGuard configs. Every site resolves to a tiny web server that
// answers with the server's fake "exit IP", so requests like
// curl -x http://user:pass@127.0.0.1:8080 http://api.ipify.org show rotation.
//
//	go run ./cmd/fakeprovider -n 3
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

func main() {
	n := flag.Int("n", 3, "number of fake servers")
	basePort := flag.Int("port", 51900, "first UDP port")
	flag.Parse()

	for i := 0; i < *n; i++ {
		priv, pub := wgtest.KeyPair()
		exit := fmt.Sprintf("203.0.113.%d", 10+i)
		p, err := wgtest.Run(exit, pub, *basePort+i)
		if err != nil {
			fmt.Fprintln(os.Stderr, "start:", err)
			os.Exit(1)
		}
		defer p.Close()
		fmt.Printf("# fake-%d (exit IP %s)\n[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = %s\n\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0\nEndpoint = %s\n\n",
			i+1, exit, priv, wgtest.ClientAddr, wgtest.DNSAddr, p.PublicKey, p.Endpoint)
	}
	fmt.Fprintln(os.Stderr, "fake providers running; Ctrl-C to stop")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
