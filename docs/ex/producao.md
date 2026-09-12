# Exemplo: Servidor de Produção de Alto Desempenho

Este exemplo orienta a implantação de um servidor **DragonTCP** em ambiente de produção (VPS Linux) preparado para suportar até **20.000 conexões simultâneas**, operando simultaneamente nas portas **53** (DNS/Direct) e **80** (HTTP/Fallback), com proteção contra exaustão de descritores de arquivos e tuning do kernel Linux.

---

## 1. Arquivo de Configuração (`/etc/dragontcp/dragontcp.yaml`)

Crie o arquivo com permissões restritas (`chmod 600 /etc/dragontcp/dragontcp.yaml`):

```yaml
# ==============================================================================
# DragonTCP Server - Configuração de Produção
# ==============================================================================

# Rede e Escuta
host: "0.0.0.0"                    # Escuta em todas as interfaces IPv4
port: 53                           # Porta principal (53 para bypass de DPI)
port_alt: 80                       # Porta secundária simultânea (HTTP)

# Segurança e Limites
token: "S3cr3t_Dr4g0n_T0k3n_2026!" # Token compartilhado obrigatório para ModeOpen
max_connections: 20000             # Limite máximo de túneis simultâneos
allow_private: false               # Bloqueia destinos RFC 1918 / Loopback

# Cache de Resolução DNS Interno
dns_cache_ttl: 60s                 # Cache mantido por 1 minuto
dns_cache_size: 8192               # Até 8192 hostnames em memória

# Performance e Buffers
tcp_buffer: 0                      # 0 = Autotuning dinâmico do kernel Linux
chunk_max: 1048576                 # Payload adaptativo até 1 MiB por frame
chunk_buffered: 32                 # Buffer de download (~2 MiB por sessão)
chunk_poll_wait: 200ms             # Espera do long-poll por dados
chunk_session_timeout: 2m          # Inatividade máxima antes de fechar sessão

# Diagnósticos e Logs
debug: false                       # Em produção, desative debug verboso
debug_chunks: false                # Nunca ative em produção (alto I/O)
debug_stats_interval: 10s          # Imprime telemetria básica a cada 10s

# API de Administração Local (para dragontcp-cli TUI)
admin_addr: "127.0.0.1:53080"      # Restrito ao loopback por segurança

# Fake SSH Integrado
ssh:
  enable: true
  listen: "127.0.0.1:2222"
  internal_host: "dragontcp-ssh.internal"
  host_key: "/etc/dragontcp/dragontcp_ssh_host_key"
  users: "/etc/dragontcp/dragontcp-users.json"

# BadVPN UDPGW Integrado (Modo ABI Linux Nativo)
udpgw:
  enable: true
  listen: "127.0.0.1:7400"
  internal_host: "dragontcp-udpgw.internal"
  max_clients: 10000
  mode: "native"                   # SO_BINDTODEVICE e SO_BUSY_POLL
  interface: "auto"                # Detecta eth0/ens3 automaticamente
  busy_poll_us: 50                 # 50 µs de polling na fila da placa de rede
  debug: false
```

---

## 2. Otimização do Kernel Linux (`/etc/sysctl.d/99-dragontcp.conf`)

Para suportar 20.000 conexões com baixa latência e evitar erros de `too many open files` ou `connection reset by peer`, configure o arquivo `/etc/sysctl.d/99-dragontcp.conf`:

```ini
# Aumenta o limite global de arquivos abertos
fs.file-max = 2097152

# Aumenta o backlog de conexões pendentes do socket
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535

# Amplia a faixa de portas efêmeras para conexões de saída
net.ipv4.ip_local_port_range = 1024 65535

# Habilita reuso de portas TIME_WAIT
net.ipv4.tcp_tw_reuse = 1

# Otimização de buffers TCP (min, default, max)
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216

# Ativa BBR para controle de congestionamento avançado
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr

# Reduz tempo de keepalive para detectar clientes mortos
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 15
net.ipv4.tcp_keepalive_probes = 5
```

Aplique as alterações imediatamente com:
```bash
sysctl --system
```

---

## 3. Limites de Processo (`/etc/security/limits.d/99-dragontcp.conf`)

```ini
* soft nofile 65536
* hard nofile 65536
root soft nofile 65536
root hard nofile 65536
```

---

## 4. Unidade Systemd (`/etc/systemd/system/dragontcp.service`)

Crie a unit para garantir que o serviço reinicie automaticamente após falhas ou reboot:

```ini
[Unit]
Description=DragonTCP High-Performance Server Daemon
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/dragontcp
ExecStart=/usr/local/bin/dragontcp-server --config /etc/dragontcp/dragontcp.yaml
Restart=always
RestartSec=3
LimitNOFILE=65536
LimitNPROC=32768

# Segurança do processo
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_NET_ADMIN
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

Ative e inicialize o serviço:
```bash
systemctl daemon-reload
systemctl enable --now dragontcp.service
```

---

## 5. Validação e Monitoramento

Verifique o status do daemon:
```bash
systemctl status dragontcp.service
```

Acompanhe os logs em tempo real:
```bash
journalctl -u dragontcp.service -f -o cat
```

Saída esperada:
```text
config=/etc/dragontcp/dragontcp.yaml
udpgw=true mode=native interface=eth0 listen=127.0.0.1:7400 internal_target=dragontcp-udpgw.internal:7400 max_clients=10000
fake_ssh=true listen=127.0.0.1:2222 internal_target=dragontcp-ssh.internal:2222 hostkey=SHA256:x9vA... users=/etc/dragontcp/dragontcp-users.json
DragonTCP Go server listening on 0.0.0.0:53
DragonTCP Go server listening on 0.0.0.0:80
```
