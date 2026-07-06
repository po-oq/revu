package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/po-oq/revu/internal/app"
)

func main() {
	cfg := app.DefaultConfig()
	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	flag.StringVar(&cfg.DataDir, "data", cfg.DataDir, "data directory")
	flag.Parse()

	handler, err := app.NewHandlerForConfig(context.Background(), cfg)
	if err != nil {
		log.Fatalf("initialize server: %v", err)
	}

	printStartup(cfg.Addr, cfg.DataDir)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatalf("listen on %s: %v", cfg.Addr, err)
	}
}

func printStartup(addr, dataDir string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Printf("revu listening on %s\n", addr)
		fmt.Printf("Data:  %s\n", dataDir)
		return
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	fmt.Printf("revu listening on %s\n", addr)
	fmt.Printf("Local: http://%s:%s\n", host, port)
	for _, ip := range localIPv4s() {
		fmt.Printf("LAN:   http://%s:%s\n", ip, port)
	}
	fmt.Printf("Data:  %s\n", dataDir)
	fmt.Println("If LAN clients cannot connect, allow revu.exe through Windows Firewall.")
}

func localIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := strings.Split(addr.String(), "/")[0]
			parsed := net.ParseIP(ip)
			if parsed == nil || parsed.To4() == nil {
				continue
			}
			out = append(out, parsed.String())
		}
	}
	return out
}
