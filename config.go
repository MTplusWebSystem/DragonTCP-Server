package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// SSHConfig defines the configuration for the fake SSH service.
type SSHConfig struct {
	Enable       bool   `yaml:"enable"`
	Listen       string `yaml:"listen"`
	InternalHost string `yaml:"internal_host"`
	HostKey      string `yaml:"host_key"`
	Users        string `yaml:"users"`
}

// UDPGWConfig defines the configuration for the BadVPN-compatible UDPGW service.
type UDPGWConfig struct {
	Enable       bool   `yaml:"enable"`
	Listen       string `yaml:"listen"`
	InternalHost string `yaml:"internal_host"`
	MaxClients   int    `yaml:"max_clients"`
	Debug        bool   `yaml:"debug"`
}

// ServerConfig defines the full configuration for the DragonTCP server.
type ServerConfig struct {
	Host           string        `yaml:"host"`
	Port           int           `yaml:"port"`
	PortAlt        int           `yaml:"port_alt"`
	Token          string        `yaml:"token"`
	MaxConnections int           `yaml:"max_connections"`
	AllowPrivate   bool          `yaml:"allow_private"`
	DNSCacheTTL    time.Duration `yaml:"dns_cache_ttl"`
	DNSCacheSize   int           `yaml:"dns_cache_size"`
	TCPBuffer      int           `yaml:"tcp_buffer"`
	ChunkMax       int           `yaml:"chunk_max"`
	ChunkBuffered  int           `yaml:"chunk_buffered"`
	ChunkPollWait  time.Duration `yaml:"chunk_poll_wait"`
	SessionTimeout time.Duration `yaml:"chunk_session_timeout"`
	Debug          bool          `yaml:"debug"`
	DebugChunks    bool          `yaml:"debug_chunks"`
	DebugStats     time.Duration `yaml:"debug_stats_interval"`

	SSH   SSHConfig   `yaml:"ssh"`
	UDPGW UDPGWConfig `yaml:"udpgw"`
}

// DefaultConfig returns the default server configuration matching CLI flag defaults.
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Host:           "0.0.0.0",
		Port:           53,
		PortAlt:        80,
		Token:          "",
		MaxConnections: 20000,
		AllowPrivate:   false,
		DNSCacheTTL:    30 * time.Second,
		DNSCacheSize:   4096,
		TCPBuffer:      0,
		ChunkMax:       1048576,
		ChunkBuffered:  32,
		ChunkPollWait:  200 * time.Millisecond,
		SessionTimeout: 2 * time.Minute,
		Debug:          false,
		DebugChunks:    false,
		DebugStats:     5 * time.Second,

		SSH: SSHConfig{
			Enable:       true,
			Listen:       defaultSSHListen,
			InternalHost: defaultSSHInternalHost,
			HostKey:      "dragontcp_ssh_host_key",
			Users:        "dragontcp-users.json",
		},

		UDPGW: UDPGWConfig{
			Enable:       true,
			Listen:       "127.0.0.1:7400",
			InternalHost: "dragontcp-udpgw.internal",
			MaxClients:   10000,
			Debug:        false,
		},
	}
}

type rawSSHConfig struct {
	Enable            *bool   `yaml:"enable"`
	Listen            *string `yaml:"listen"`
	InternalHost      *string `yaml:"internal_host"`
	InternalHostKebab *string `yaml:"internal-host"`
	HostKey           *string `yaml:"host_key"`
	HostKeyKebab      *string `yaml:"host-key"`
	Users             *string `yaml:"users"`
}

type rawUDPGWConfig struct {
	Enable            *bool   `yaml:"enable"`
	Listen            *string `yaml:"listen"`
	InternalHost      *string `yaml:"internal_host"`
	InternalHostKebab *string `yaml:"internal-host"`
	MaxClients        *int    `yaml:"max_clients"`
	MaxClientsKebab   *int    `yaml:"max-clients"`
	Debug             *bool   `yaml:"debug"`
}

type rawServerConfig struct {
	Host                *string        `yaml:"host"`
	Port                *int           `yaml:"port"`
	PortAlt             *int           `yaml:"port_alt"`
	PortAltKebab        *int           `yaml:"port-alt"`
	Token               *string        `yaml:"token"`
	MaxConnections      *int           `yaml:"max_connections"`
	MaxConnectionsKebab *int           `yaml:"max-connections"`
	AllowPrivate        *bool          `yaml:"allow_private"`
	AllowPrivateKebab   *bool          `yaml:"allow-private"`
	DNSCacheTTL         *time.Duration `yaml:"dns_cache_ttl"`
	DNSCacheTTLKebab    *time.Duration `yaml:"dns-cache-ttl"`
	DNSCacheSize        *int           `yaml:"dns_cache_size"`
	DNSCacheSizeKebab   *int           `yaml:"dns-cache-size"`
	TCPBuffer           *int           `yaml:"tcp_buffer"`
	TCPBufferKebab      *int           `yaml:"tcp-buffer"`
	ChunkMax            *int           `yaml:"chunk_max"`
	ChunkMaxKebab       *int           `yaml:"chunk-max"`
	ChunkBuffered       *int           `yaml:"chunk_buffered"`
	ChunkBufferedKebab  *int           `yaml:"chunk-buffered"`
	ChunkPollWait       *time.Duration `yaml:"chunk_poll_wait"`
	ChunkPollWaitKebab  *time.Duration `yaml:"chunk-poll-wait"`
	SessionTimeout      *time.Duration `yaml:"chunk_session_timeout"`
	SessionTimeoutKebab *time.Duration `yaml:"chunk-session-timeout"`
	Debug               *bool          `yaml:"debug"`
	DebugChunks         *bool          `yaml:"debug_chunks"`
	DebugChunksKebab    *bool          `yaml:"debug-chunks"`
	DebugStats          *time.Duration `yaml:"debug_stats_interval"`
	DebugStatsKebab     *time.Duration `yaml:"debug-stats-interval"`

	SSH                *rawSSHConfig `yaml:"ssh"`
	SSHEnableFlat      *bool         `yaml:"ssh_enable"`
	SSHEnableFlatKebab *bool         `yaml:"ssh-enable"`
	SSHListenFlat      *string       `yaml:"ssh_listen"`
	SSHListenFlatKebab *string       `yaml:"ssh-listen"`
	SSHInternalHostFlat      *string `yaml:"ssh_internal_host"`
	SSHInternalHostFlatKebab *string `yaml:"ssh-internal-host"`
	SSHHostKeyFlat           *string `yaml:"ssh_host_key"`
	SSHHostKeyFlatKebab      *string `yaml:"ssh-host-key"`
	SSHUsersFlat             *string `yaml:"ssh_users"`
	SSHUsersFlatKebab        *string `yaml:"ssh-users"`

	UDPGW                      *rawUDPGWConfig `yaml:"udpgw"`
	UDPGWEnableFlat            *bool           `yaml:"udpgw_enable"`
	UDPGWEnableFlatKebab       *bool           `yaml:"udpgw-enable"`
	UDPGWListenFlat            *string         `yaml:"udpgw_listen"`
	UDPGWListenFlatKebab       *string         `yaml:"udpgw-listen"`
	UDPGWInternalHostFlat      *string         `yaml:"udpgw_internal_host"`
	UDPGWInternalHostFlatKebab *string         `yaml:"udpgw-internal-host"`
	UDPGWMaxClientsFlat        *int            `yaml:"udpgw_max_clients"`
	UDPGWMaxClientsFlatKebab   *int            `yaml:"udpgw-max-clients"`
	UDPGWDebugFlat             *bool           `yaml:"udpgw_debug"`
	UDPGWDebugFlatKebab        *bool           `yaml:"udpgw-debug"`
}

// LoadConfigFile reads and parses a YAML configuration file.
func LoadConfigFile(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return LoadConfigBytes(data)
}

// LoadConfigBytes parses YAML configuration bytes into ServerConfig,
// starting from default values and merging any specified keys.
func LoadConfigBytes(data []byte) (*ServerConfig, error) {
	var raw rawServerConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse yaml config: %w", err)
	}

	cfg := DefaultConfig()

	if raw.Host != nil {
		cfg.Host = *raw.Host
	}
	if raw.Port != nil {
		cfg.Port = *raw.Port
	}
	if raw.PortAlt != nil {
		cfg.PortAlt = *raw.PortAlt
	} else if raw.PortAltKebab != nil {
		cfg.PortAlt = *raw.PortAltKebab
	}
	if raw.Token != nil {
		cfg.Token = *raw.Token
	}
	if raw.MaxConnections != nil {
		cfg.MaxConnections = *raw.MaxConnections
	} else if raw.MaxConnectionsKebab != nil {
		cfg.MaxConnections = *raw.MaxConnectionsKebab
	}
	if raw.AllowPrivate != nil {
		cfg.AllowPrivate = *raw.AllowPrivate
	} else if raw.AllowPrivateKebab != nil {
		cfg.AllowPrivate = *raw.AllowPrivateKebab
	}
	if raw.DNSCacheTTL != nil {
		cfg.DNSCacheTTL = *raw.DNSCacheTTL
	} else if raw.DNSCacheTTLKebab != nil {
		cfg.DNSCacheTTL = *raw.DNSCacheTTLKebab
	}
	if raw.DNSCacheSize != nil {
		cfg.DNSCacheSize = *raw.DNSCacheSize
	} else if raw.DNSCacheSizeKebab != nil {
		cfg.DNSCacheSize = *raw.DNSCacheSizeKebab
	}
	if raw.TCPBuffer != nil {
		cfg.TCPBuffer = *raw.TCPBuffer
	} else if raw.TCPBufferKebab != nil {
		cfg.TCPBuffer = *raw.TCPBufferKebab
	}
	if raw.ChunkMax != nil {
		cfg.ChunkMax = *raw.ChunkMax
	} else if raw.ChunkMaxKebab != nil {
		cfg.ChunkMax = *raw.ChunkMaxKebab
	}
	if raw.ChunkBuffered != nil {
		cfg.ChunkBuffered = *raw.ChunkBuffered
	} else if raw.ChunkBufferedKebab != nil {
		cfg.ChunkBuffered = *raw.ChunkBufferedKebab
	}
	if raw.ChunkPollWait != nil {
		cfg.ChunkPollWait = *raw.ChunkPollWait
	} else if raw.ChunkPollWaitKebab != nil {
		cfg.ChunkPollWait = *raw.ChunkPollWaitKebab
	}
	if raw.SessionTimeout != nil {
		cfg.SessionTimeout = *raw.SessionTimeout
	} else if raw.SessionTimeoutKebab != nil {
		cfg.SessionTimeout = *raw.SessionTimeoutKebab
	}
	if raw.Debug != nil {
		cfg.Debug = *raw.Debug
	}
	if raw.DebugChunks != nil {
		cfg.DebugChunks = *raw.DebugChunks
	} else if raw.DebugChunksKebab != nil {
		cfg.DebugChunks = *raw.DebugChunksKebab
	}
	if raw.DebugStats != nil {
		cfg.DebugStats = *raw.DebugStats
	} else if raw.DebugStatsKebab != nil {
		cfg.DebugStats = *raw.DebugStatsKebab
	}

	// SSH section: nested takes precedence over flat if present
	if raw.SSH != nil {
		if raw.SSH.Enable != nil {
			cfg.SSH.Enable = *raw.SSH.Enable
		}
		if raw.SSH.Listen != nil {
			cfg.SSH.Listen = *raw.SSH.Listen
		}
		if raw.SSH.InternalHost != nil {
			cfg.SSH.InternalHost = *raw.SSH.InternalHost
		} else if raw.SSH.InternalHostKebab != nil {
			cfg.SSH.InternalHost = *raw.SSH.InternalHostKebab
		}
		if raw.SSH.HostKey != nil {
			cfg.SSH.HostKey = *raw.SSH.HostKey
		} else if raw.SSH.HostKeyKebab != nil {
			cfg.SSH.HostKey = *raw.SSH.HostKeyKebab
		}
		if raw.SSH.Users != nil {
			cfg.SSH.Users = *raw.SSH.Users
		}
	}
	if raw.SSHEnableFlat != nil {
		cfg.SSH.Enable = *raw.SSHEnableFlat
	} else if raw.SSHEnableFlatKebab != nil {
		cfg.SSH.Enable = *raw.SSHEnableFlatKebab
	}
	if raw.SSHListenFlat != nil {
		cfg.SSH.Listen = *raw.SSHListenFlat
	} else if raw.SSHListenFlatKebab != nil {
		cfg.SSH.Listen = *raw.SSHListenFlatKebab
	}
	if raw.SSHInternalHostFlat != nil {
		cfg.SSH.InternalHost = *raw.SSHInternalHostFlat
	} else if raw.SSHInternalHostFlatKebab != nil {
		cfg.SSH.InternalHost = *raw.SSHInternalHostFlatKebab
	}
	if raw.SSHHostKeyFlat != nil {
		cfg.SSH.HostKey = *raw.SSHHostKeyFlat
	} else if raw.SSHHostKeyFlatKebab != nil {
		cfg.SSH.HostKey = *raw.SSHHostKeyFlatKebab
	}
	if raw.SSHUsersFlat != nil {
		cfg.SSH.Users = *raw.SSHUsersFlat
	} else if raw.SSHUsersFlatKebab != nil {
		cfg.SSH.Users = *raw.SSHUsersFlatKebab
	}

	// UDPGW section: nested takes precedence over flat if present
	if raw.UDPGW != nil {
		if raw.UDPGW.Enable != nil {
			cfg.UDPGW.Enable = *raw.UDPGW.Enable
		}
		if raw.UDPGW.Listen != nil {
			cfg.UDPGW.Listen = *raw.UDPGW.Listen
		}
		if raw.UDPGW.InternalHost != nil {
			cfg.UDPGW.InternalHost = *raw.UDPGW.InternalHost
		} else if raw.UDPGW.InternalHostKebab != nil {
			cfg.UDPGW.InternalHost = *raw.UDPGW.InternalHostKebab
		}
		if raw.UDPGW.MaxClients != nil {
			cfg.UDPGW.MaxClients = *raw.UDPGW.MaxClients
		} else if raw.UDPGW.MaxClientsKebab != nil {
			cfg.UDPGW.MaxClients = *raw.UDPGW.MaxClientsKebab
		}
		if raw.UDPGW.Debug != nil {
			cfg.UDPGW.Debug = *raw.UDPGW.Debug
		}
	}
	if raw.UDPGWEnableFlat != nil {
		cfg.UDPGW.Enable = *raw.UDPGWEnableFlat
	} else if raw.UDPGWEnableFlatKebab != nil {
		cfg.UDPGW.Enable = *raw.UDPGWEnableFlatKebab
	}
	if raw.UDPGWListenFlat != nil {
		cfg.UDPGW.Listen = *raw.UDPGWListenFlat
	} else if raw.UDPGWListenFlatKebab != nil {
		cfg.UDPGW.Listen = *raw.UDPGWListenFlatKebab
	}
	if raw.UDPGWInternalHostFlat != nil {
		cfg.UDPGW.InternalHost = *raw.UDPGWInternalHostFlat
	} else if raw.UDPGWInternalHostFlatKebab != nil {
		cfg.UDPGW.InternalHost = *raw.UDPGWInternalHostFlatKebab
	}
	if raw.UDPGWMaxClientsFlat != nil {
		cfg.UDPGW.MaxClients = *raw.UDPGWMaxClientsFlat
	} else if raw.UDPGWMaxClientsFlatKebab != nil {
		cfg.UDPGW.MaxClients = *raw.UDPGWMaxClientsFlatKebab
	}
	if raw.UDPGWDebugFlat != nil {
		cfg.UDPGW.Debug = *raw.UDPGWDebugFlat
	} else if raw.UDPGWDebugFlatKebab != nil {
		cfg.UDPGW.Debug = *raw.UDPGWDebugFlatKebab
	}

	return &cfg, nil
}

// serverFlagTargets maps flag names to pointers so that unvisited flags
// can be populated from the parsed configuration file.
type serverFlagTargets struct {
	host              *string
	port              *int
	portAlt           *int
	token             *string
	maxConnections    *int
	allowPrivate      *bool
	dnsCacheTTL       *time.Duration
	dnsCacheSize      *int
	tcpBuffer         *int
	chunkMax          *int
	chunkBuffered     *int
	chunkPollWait     *time.Duration
	sessionTimeout    *time.Duration
	debugEnabled      *bool
	debugChunks       *bool
	debugStats        *time.Duration
	sshEnable         *bool
	sshListen         *string
	sshInternalHost   *string
	sshHostKey        *string
	sshUsers          *string
	udpgwEnable       *bool
	udpgwListen       *string
	udpgwInternalHost *string
	udpgwMaxClients   *int
	udpgwDebug        *bool
}

// applyConfig merges values from cfg into the flag pointers only if the flag
// was not explicitly set on the command line (checked via visited map).
func applyConfig(cfg *ServerConfig, visited map[string]bool, t serverFlagTargets) {
	if !visited["host"] && t.host != nil {
		*t.host = cfg.Host
	}
	if !visited["port"] && t.port != nil {
		*t.port = cfg.Port
	}
	if !visited["port-alt"] && t.portAlt != nil {
		*t.portAlt = cfg.PortAlt
	}
	if !visited["token"] && t.token != nil {
		*t.token = cfg.Token
	}
	if !visited["max-connections"] && t.maxConnections != nil {
		*t.maxConnections = cfg.MaxConnections
	}
	if !visited["allow-private"] && t.allowPrivate != nil {
		*t.allowPrivate = cfg.AllowPrivate
	}
	if !visited["dns-cache-ttl"] && t.dnsCacheTTL != nil {
		*t.dnsCacheTTL = cfg.DNSCacheTTL
	}
	if !visited["dns-cache-size"] && t.dnsCacheSize != nil {
		*t.dnsCacheSize = cfg.DNSCacheSize
	}
	if !visited["tcp-buffer"] && t.tcpBuffer != nil {
		*t.tcpBuffer = cfg.TCPBuffer
	}
	if !visited["chunk-max"] && t.chunkMax != nil {
		*t.chunkMax = cfg.ChunkMax
	}
	if !visited["chunk-buffered"] && t.chunkBuffered != nil {
		*t.chunkBuffered = cfg.ChunkBuffered
	}
	if !visited["chunk-poll-wait"] && t.chunkPollWait != nil {
		*t.chunkPollWait = cfg.ChunkPollWait
	}
	if !visited["chunk-session-timeout"] && t.sessionTimeout != nil {
		*t.sessionTimeout = cfg.SessionTimeout
	}
	if !visited["debug"] && t.debugEnabled != nil {
		*t.debugEnabled = cfg.Debug
	}
	if !visited["debug-chunks"] && t.debugChunks != nil {
		*t.debugChunks = cfg.DebugChunks
	}
	if !visited["debug-stats-interval"] && t.debugStats != nil {
		*t.debugStats = cfg.DebugStats
	}

	// SSH
	if !visited["ssh-enable"] && t.sshEnable != nil {
		*t.sshEnable = cfg.SSH.Enable
	}
	if !visited["ssh-listen"] && t.sshListen != nil {
		*t.sshListen = cfg.SSH.Listen
	}
	if !visited["ssh-internal-host"] && t.sshInternalHost != nil {
		*t.sshInternalHost = cfg.SSH.InternalHost
	}
	if !visited["ssh-host-key"] && t.sshHostKey != nil {
		*t.sshHostKey = cfg.SSH.HostKey
	}
	if !visited["ssh-users"] && t.sshUsers != nil {
		*t.sshUsers = cfg.SSH.Users
	}

	// UDPGW
	if !visited["udpgw-enable"] && t.udpgwEnable != nil {
		*t.udpgwEnable = cfg.UDPGW.Enable
	}
	if !visited["udpgw-listen"] && t.udpgwListen != nil {
		*t.udpgwListen = cfg.UDPGW.Listen
	}
	if !visited["udpgw-internal-host"] && t.udpgwInternalHost != nil {
		*t.udpgwInternalHost = cfg.UDPGW.InternalHost
	}
	if !visited["udpgw-max-clients"] && t.udpgwMaxClients != nil {
		*t.udpgwMaxClients = cfg.UDPGW.MaxClients
	}
	if !visited["udpgw-debug"] && t.udpgwDebug != nil {
		*t.udpgwDebug = cfg.UDPGW.Debug
	}
}
