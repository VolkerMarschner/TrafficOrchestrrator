package main

import "time"

// Logging
const (
	defaultLogMaxSizeMB = 10
	defaultLogMaxFiles  = 5
)

// Timing
const (
	connectTimeout         = 5 * time.Second
	tcpHoldDuration        = 10 * time.Millisecond
	defaultConnectionDelay = 100 * time.Millisecond
	heartbeatInterval      = 30 * time.Second
	heartbeatCheck         = 1 * time.Minute
	reconnectDelay         = 5 * time.Second
	configWatchInterval    = 5 * time.Second
)

// Distribution & registry (v0.4.5)
const (
	// distributionPort is the HTTP port on which the master serves its binary
	// for agent self-update and exposes the agent registry endpoint.
	distributionPort = 9001

	// registryFile is the JSON file written by the master to track all agents.
	registryFile = "agents.json"

	// pidFile is written by a process started with -d/--daemon.
	pidFile = "trafficorch.pid"
)
