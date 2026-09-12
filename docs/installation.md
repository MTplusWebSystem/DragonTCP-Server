# DragonTCP — Guia Completo de Instalação e Implantação

Este guia fornece o passo a passo detalhado para instalar, compilar, configurar e gerenciar em produção o **DragonTCP Server** e a ferramenta administrativa **DragonTCP CLI** em distribuições Linux e servidores em nuvem.

---

## Sumário

1. [Requisitos de Sistema](#-requisitos-de-sistema)
2. [Método 1: Instalação Automatizada (Script Shell)](#-método-1-instalação-automatizada-script-shell)
3. [Método 2: Instalação Manual Passo a Passo](#-método-2-instalação-manual-passo-a-passo)
4. [Configuração do Serviço Systemd](#-configuração-do-serviço-systemd)
5. [Configuração de Firewall e Portas](#-configuração-de-firewall-e-portas)
6. [Otimização do Kernel (Sysctl)](#-otimização-do-kernel-sysctl)
7. [Verificação e Pós-Instalação](#-verificação-e-pós-instalação)
8. [Atualização e Desinstalação](#-atualização-e-desinstalação)

---

## 💻 Requisitos de Sistema

### Distribuições Linux Suportadas
* **Ubuntu:** 20.04 LTS, 22.04 LTS, 24.04 LTS
* **Debian:** 11 (Bullseye), 12 (Bookworm)
* **RHEL / CentOS / AlmaLinux / Rocky Linux:** 8.x, 9.x
* **Alpine Linux:** 3.18+
* **Arch Linux:** Atualizado

### Arquiteturas Suportadas
* `x86_64` / `amd64` (Servidores convencionais Intel/AMD)
* `aarch64` / `arm64` (Servidores ARM, Raspberry Pi 4/5, instâncias Graviton/Ampere)
* `armv7` / `armhf` e `i386` / `386`

### Requisitos de Hardware
* **Mínimo:** 1 vCPU, 512 MB de RAM, 100 MB de disco.
* **Recomendado (Produção > 5.000 conexões):** 2 vCPUs ou mais, 2 GB de RAM, link de rede de 1 Gbps.
* **Software:** Go (Golang) versão `1.22+` (necessário caso for compilar a partir do código-fonte), `git`, `curl` e privilégios de `root`.

---

## 🚀 Método 1: Instalação Automatizada (Script Shell)

O repositório inclui um script instalador oficial autônomo ([install.sh](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/install.sh)) que realiza todo o processo automaticamente:
* Instala dependências do sistema.
* Compila os binários `dragontcp-server` e `dragontcp-cli` com otimizações `-ldflags="-s -w"`.
* Instala os executáveis em `/usr/local/bin/`.
* Cria o atalho global de terminal `dragontcp`.
* Gera a pasta `/etc/dragontcp`, o arquivo `dragontcp.yaml`, a chave SSH ed25519 e o banco `dragontcp-users.json`.
* Configura e inicia o serviço `systemd`.
* Aplica regras de firewall e ajustes de kernel.

### Executar a Instalação Automatizada:

```bash
# Baixar ou clonar o projeto e executar como root:
chmod +x install.sh
sudo ./install.sh
```

Ou execute diretamente em modo não-interativo com o argumento `install`:
```bash
sudo ./install.sh install
```

---

## 🛠️ Método 2: Instalação Manual Passo a Passo

Caso prefira configurar cada elemento manualmente:

### Passo 1: Instalar Dependências e Go Compiler (Ubuntu/Debian)

```bash
sudo apt update && sudo apt install -y git curl tar build-essential ufw

# Instalar Go mais recente se ainda não estiver instalado:
GO_VERSION="1.23.1"
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o go.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go.tar.gz
rm -f go.tar.gz

# Adicionar Go ao PATH (se necessário)
export PATH=$PATH:/usr/local/go
echo 'export PATH=$PATH:/usr/local/go' >> ~/.bashrc
```

### Passo 2: Clonar o Repositório do DragonTCP

```bash
git clone https://github.com/seu-usuario/dragontcp.git /opt/dragontcp
cd /opt/dragontcp/core/cmd/dragontcp-server
```

### Passo 3: Compilar o Servidor e a CLI com Otimizações

O uso de `-ldflags="-s -w"` remove a tabela de símbolos e informações de depuração DWARF, reduzindo o tamanho do binário final em até 40%:

```bash
# Compilar DragonTCP Server
go build -v -ldflags="-s -w" -o /usr/local/bin/dragontcp-server .
sudo chmod +x /usr/local/bin/dragontcp-server

# Compilar DragonTCP CLI TUI
go build -v -ldflags="-s -w" -o /usr/local/bin/dragontcp-cli ./cli
sudo chmod +x /usr/local/bin/dragontcp-cli

# Criar atalho global 'dragontcp'
sudo ln -sf /usr/local/bin/dragontcp-cli /usr/local/bin/dragontcp
```

### Passo 4: Criar Estrutura de Diretórios de Sistema

```bash
sudo mkdir -p /etc/dragontcp
sudo mkdir -p /var/log/dragontcp
sudo chmod 750 /etc/dragontcp
```

### Passo 5: Criar o Arquivo de Configuração Principal

Copie o modelo de produção ou crie `/etc/dragontcp/dragontcp.yaml`:

```bash
sudo cp dragontcp.example.yaml /etc/dragontcp/dragontcp.yaml
sudo chmod 600 /etc/dragontcp/dragontcp.yaml
```

Certifique-se de ajustar o arquivo com os caminhos corretos:
```yaml
host: "0.0.0.0"
port: 53
port_alt: 80
token: ""
max_connections: 20000
allow_private: false

ssh:
  enable: true
  listen: "127.0.0.1:2222"
  internal_host: "dragontcp-ssh.internal"
  host_key: "/etc/dragontcp/dragontcp_ssh_host_key"
  users: "/etc/dragontcp/dragontcp-users.json"

udpgw:
  enable: true
  listen: "127.0.0.1:7400"
  internal_host: "dragontcp-udpgw.internal"
  max_clients: 10000
  mode: "native"
  interface: "auto"
  busy_poll_us: 50
```

### Passo 6: Gerar Chave Privada do Fake SSH e Banco de Usuários

```bash
# Gerar chave host privada SSH do tipo ED25519 (se ausente):
if [ ! -f /etc/dragontcp/dragontcp_ssh_host_key ]; then
  ssh-keygen -t ed25519 -f /etc/dragontcp/dragontcp_ssh_host_key -N "" -q
  sudo chmod 600 /etc/dragontcp/dragontcp_ssh_host_key
fi

# Inicializar banco de usuários com um usuário inicial de teste:
sudo dragontcp-server --config /etc/dragontcp/dragontcp.yaml \
  --ssh-user-add "admin" \
  --ssh-user-password "Admin@Dragon2026" \
  --ssh-user-days 0 \
  --ssh-user-max-connections 0
```

---

## ⚙️ Configuração do Serviço Systemd

Crie o arquivo `/etc/systemd/system/dragontcp.service`:

```ini
[Unit]
Description=DragonTCP High-Performance Server Daemon
Documentation=https://github.com
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

# Permissões do processo
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_NET_ADMIN
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

### Ativar e Iniciar o Serviço:
```bash
sudo systemctl daemon-reload
sudo systemctl enable dragontcp.service
sudo systemctl start dragontcp.service
sudo systemctl status dragontcp.service
```

---

## 🛡️ Configuração de Firewall e Portas

Libere as portas públicas utilizadas pelo DragonTCP:

### Com UFW (Ubuntu / Debian):
```bash
# Permitir porta principal (53 TCP)
sudo ufw allow 53/tcp comment 'DragonTCP Porta Principal'

# Permitir porta secundária (80 TCP)
sudo ufw allow 80/tcp comment 'DragonTCP Porta Alternativa / HTTP'

# Se o UFW estiver desativado, ative:
sudo ufw --force enable
sudo ufw status verbose
```

### Com Firewalld (CentOS / RHEL / AlmaLinux):
```bash
sudo firewall-cmd --permanent --add-port=53/tcp
sudo firewall-cmd --permanent --add-port=80/tcp
sudo firewall-cmd --reload
```

> [!NOTE]
> As portas `2222` (Fake SSH), `7400` (UDPGW) e `53080` (API Admin) rodam exclusivamente em loopback (`127.0.0.1`) e **não devem ser expostas no firewall público**, garantindo segurança absoluta.

---

## 🚀 Otimização do Kernel (Sysctl)

Crie o arquivo `/etc/sysctl.d/99-dragontcp.conf`:

```ini
# Limite máximo de descritores de arquivos
fs.file-max = 2097152

# Backlog de conexões do socket
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535

# Reuso de portas e faixas de portas locais
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1

# Tamanhos de buffers de rede
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216

# Algoritmo de congestionamento BBR
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
```

Aplique imediatamente sem reiniciar:
```bash
sudo sysctl --system
```

---

## 🔍 Verificação e Pós-Instalação

1. **Acompanhe os logs em tempo real:**
   ```bash
   sudo journalctl -u dragontcp.service -f
   ```

2. **Inicie a CLI Administrativa TUI:**
   ```bash
   dragontcp
   ```
   *(Ou execute `dragontcp-cli`)*

3. **Verifique os sockets abertos no sistema:**
   ```bash
   sudo ss -tlpn | grep dragontcp
   ```
   Deverá listar as portas `53`, `80`, `2222`, `7400` e `53080`.

---

## 🔄 Atualização e Desinstalação

### Como Atualizar o DragonTCP:
Se estiver utilizando o script instalador:
```bash
sudo ./install.sh update
```
Ou manualmente recompilando e substituindo `/usr/local/bin/dragontcp-server` e reiniciando o serviço (`systemctl restart dragontcp`).

### Como Desinstalar:
Com o script instalador:
```bash
sudo ./install.sh uninstall
```
Ou manualmente:
```bash
sudo systemctl stop dragontcp.service
sudo systemctl disable dragontcp.service
sudo rm -f /etc/systemd/system/dragontcp.service
sudo systemctl daemon-reload
sudo rm -f /usr/local/bin/dragontcp-server /usr/local/bin/dragontcp-cli /usr/local/bin/dragontcp
# Se desejar remover configurações e dados de usuários:
# sudo rm -rf /etc/dragontcp /var/log/dragontcp
```
