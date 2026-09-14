package config

import (
	"flag"
	"fmt"
	"os"
)

type Config struct {
	Host string
	Port string
}

func Load() (*Config, error) {
	defaultHost := os.Getenv("HOST")
	if defaultHost == "" {
		defaultHost = "127.0.0.1" // Strict localhost binding
	}

	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}

	host := flag.String("host", defaultHost, "Host address to bind to (strictly defaults to 127.0.0.1)")
	port := flag.String("port", defaultPort, "Port to listen on")
	flag.Parse()

	if *host == "0.0.0.0" {
		return nil, fmt.Errorf("insecure binding error: binding to 0.0.0.0 is prohibited. Specify a loopback address (e.g. 127.0.0.1) or an explicit private network IP")
	}

	return &Config{
		Host: *host,
		Port: *port,
	}, nil
}

func (c *Config) Address() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}
