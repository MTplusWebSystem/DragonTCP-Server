#!/usr/bin/env bash
# ==============================================================================
# DragonTCP Server & CLI - Script de Instalação e Gerenciamento Oficial
# ==============================================================================
# Compatibilidade: Ubuntu, Debian, CentOS, AlmaLinux, Rocky, Alpine, Arch
# Arquiteturas: amd64 (x86_64), arm64 (aarch64), armv7 (armhf), 386 (i386)
# ==============================================================================

set -eo pipefail

# Cores e Estilos
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
NC='\033[0m' # Sem cor

# Constantes de Diretórios e Arquivos
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/dragontcp"
LOG_DIR="/var/log/dragontcp"
CONFIG_FILE="${CONFIG_DIR}/dragontcp.yaml"
USERS_FILE="${CONFIG_DIR}/dragontcp-users.json"
SSH_KEY_FILE="${CONFIG_DIR}/dragontcp_ssh_host_key"
SYSTEMD_SERVICE="/etc/systemd/system/dragontcp.service"
SYSCTL_FILE="/etc/sysctl.d/99-dragontcp.conf"

SERVER_BIN="${INSTALL_DIR}/dragontcp-server"
CLI_BIN="${INSTALL_DIR}/dragontcp-cli"
CLI_SYMLINK="${INSTALL_DIR}/dragontcp"

GO_MIN_VERSION="1.22"
GO_INSTALL_VERSION="1.23.1"

# ── Funções de Interface e Logs ────────────────────────────────────────────────

print_banner() {
    clear 2>/dev/null || true
    echo -e "${CYAN}"
    echo "  ██████╗ ██████╗  █████╗  ██████╗  ██████╗ ███╗   ██╗████████╗ ██████╗██████╗ "
    echo "  ██╔══██╗██╔══██╗██╔══██╗██╔════╝ ██╔═══██╗████╗  ██║╚══██╔══╝██╔════╝██╔══██╗"
    echo "  ██║  ██║██████╔╝███████║██║  ███╗██║   ██║██╔██╗ ██║   ██║   ██║     ██████╔╝"
    echo "  ██║  ██║██╔══██╗██╔══██║██║   ██║██║   ██║██║╚██╗██║   ██║   ██║     ██╔═══╝ "
    echo "  ██████╔╝██║  ██║██║  ██║╚██████╔╝╚██████╔╝██║ ╚████║   ██║   ╚██████╗██║     "
    echo "  ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝   ╚═╝    ╚═════╝╚═╝     "
    echo -e "${PURPLE}         DragonTCP Protocol v2 Multiplexed Server & TUI CLI Installer${NC}"
    echo -e "${BLUE}==============================================================================${NC}"
}

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[AVISO]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERRO]${NC} $1"
}

# ── Verificação de Privilégios e Ambiente ──────────────────────────────────────

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "Este instalador deve ser executado com privilégios de root (sudo)."
        exit 1
    fi
}

detect_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64)
            GOARCH="amd64"
            ;;
        aarch64|arm64)
            GOARCH="arm64"
            ;;
        armv7l|armhf)
            GOARCH="armv6l"
            ;;
        i386|i686)
            GOARCH="386"
            ;;
        *)
            log_error "Arquitetura não suportada: $ARCH"
            exit 1
            ;;
    esac
    log_info "Arquitetura detectada: ${WHITE}${ARCH}${NC} (Go target: ${GOARCH})"
}

detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO=$ID
        DISTRO_FAMILY=${ID_LIKE:-$ID}
    elif [ -f /etc/debian_version ]; then
        DISTRO="debian"
        DISTRO_FAMILY="debian"
    elif [ -f /etc/redhat-release ]; then
        DISTRO="rhel"
        DISTRO_FAMILY="rhel"
    else
        DISTRO="unknown"
        DISTRO_FAMILY="unknown"
    fi
    log_info "Distribuição detectada: ${WHITE}${DISTRO}${NC}"
}

# ── Instalação de Dependências do Sistema ──────────────────────────────────────

install_packages() {
    log_info "Atualizando repositórios e instalando dependências de sistema..."
    case "$DISTRO" in
        ubuntu|debian)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq git curl tar build-essential ufw openssl
            ;;
        centos|rhel|almalinux|rocky|fedora)
            if command -v dnf >/dev/null 2>&1; then
                dnf install -y -q git curl tar gcc make openssl
            else
                yum install -y -q git curl tar gcc make openssl
            fi
            ;;
        alpine)
            apk update -q
            apk add -q git curl tar gcc make musl-dev bash openssl
            ;;
        arch)
            pacman -Sy --noconfirm git curl tar gcc make openssl
            ;;
        *)
            log_warn "Distribuição não mapeada diretamente. Tentando continuar..."
            ;;
    esac
    log_success "Dependências do sistema instaladas com sucesso."
}

# ── Verificação e Instalação do Compilador Go ──────────────────────────────────

ensure_golang() {
    local need_install=0

    if command -v go >/dev/null 2>&1; then
        CURRENT_GO_VER=$(go version | awk '{print $3}' | sed 's/go//')
        log_info "Compilador Go detectado: ${WHITE}v${CURRENT_GO_VER}${NC}"
        # Comparação básica de versão
        CURRENT_MAJOR_MINOR=$(echo "$CURRENT_GO_VER" | cut -d. -f1,2)
        if [[ "$(printf '%s\n' "$GO_MIN_VERSION" "$CURRENT_MAJOR_MINOR" | sort -V | head -n1)" != "$GO_MIN_VERSION" ]]; then
            log_warn "Versão do Go ($CURRENT_GO_VER) é inferior à mínima requerida ($GO_MIN_VERSION). Atualizando..."
            need_install=1
        fi
    else
        log_warn "Compilador Go não encontrado no sistema. Instalando Go v${GO_INSTALL_VERSION}..."
        need_install=1
    fi

    if [[ $need_install -eq 1 ]]; then
        local GO_TAR="go${GO_INSTALL_VERSION}.linux-${GOARCH}.tar.gz"
        local GO_URL="https://go.dev/dl/${GO_TAR}"

        log_info "Baixando ${GO_URL}..."
        curl -fsSL "$GO_URL" -o "/tmp/${GO_TAR}"

        log_info "Instalando Go em /usr/local/go..."
        rm -rf /usr/local/go
        tar -C /usr/local -xzf "/tmp/${GO_TAR}"
        rm -f "/tmp/${GO_TAR}"

        export PATH="/usr/local/go/bin:$PATH"
        if ! grep -q '/usr/local/go/bin' /etc/profile; then
            echo 'export PATH=/usr/local/go/bin:$PATH' >> /etc/profile
        fi
        if ! grep -q '/usr/local/go/bin' /root/.bashrc; then
            echo 'export PATH=/usr/local/go/bin:$PATH' >> /root/.bashrc
        fi
        log_success "Golang v${GO_INSTALL_VERSION} instalado com sucesso!"
    fi
}

# ── Compilação dos Binários DragonTCP ──────────────────────────────────────────

build_binaries() {
    # Determina o diretório base do projeto (onde está o main.go do servidor)
    local SCRIPT_DIR
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local SRC_DIR=""

    if [[ -f "${SCRIPT_DIR}/main.go" && -d "${SCRIPT_DIR}/cli" ]]; then
        SRC_DIR="$SCRIPT_DIR"
    elif [[ -f "/opt/dragontcp/core/cmd/dragontcp-server/main.go" ]]; then
        SRC_DIR="/opt/dragontcp/core/cmd/dragontcp-server"
    elif [[ -f "./main.go" ]]; then
        SRC_DIR="$(pwd)"
    else
        log_info "Código-fonte não encontrado localmente. Clonando repositório..."
        rm -rf /opt/dragontcp
        git clone https://github.com/MTplusWebSystem/DragonTCP-Server.git /opt/dragontcp || git clone https://github.com/dragontcp/dragontcp.git /opt/dragontcp
        SRC_DIR="/opt/dragontcp/core/cmd/dragontcp-server"
    fi

    log_info "Compilando DragonTCP Server a partir de: ${WHITE}${SRC_DIR}${NC}..."
    cd "$SRC_DIR"

    # Baixar módulos go
    go mod download

    # Compilar servidor
    log_info "Compilando servidor (flags: -ldflags=\"-s -w\")..."
    go build -v -ldflags="-s -w" -o "${SERVER_BIN}" .
    chmod 755 "${SERVER_BIN}"
    log_success "Servidor compilado e instalado em: ${SERVER_BIN}"

    # Compilar CLI
    log_info "Compilando DragonTCP CLI TUI..."
    go build -v -ldflags="-s -w" -o "${CLI_BIN}" ./cli
    chmod 755 "${CLI_BIN}"
    ln -sf "${CLI_BIN}" "${CLI_SYMLINK}"
    log_success "CLI compilada e instalada em: ${CLI_BIN} (atalho: ${CLI_SYMLINK})"
}

# ── Configuração de Diretórios, Chaves e Arquivos ──────────────────────────────

setup_config_and_files() {
    log_info "Configurando diretórios em ${CONFIG_DIR}..."
    mkdir -p "${CONFIG_DIR}"
    mkdir -p "${LOG_DIR}"
    chmod 750 "${CONFIG_DIR}"
    chmod 750 "${LOG_DIR}"

    # 1. dragontcp.yaml
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        log_info "Criando arquivo de configuração padrão em ${CONFIG_FILE}..."
        cat > "${CONFIG_FILE}" << 'EOF'
# ==============================================================================
# DragonTCP Server - Arquivo de Configuração de Produção
# ==============================================================================

# Rede e Escuta
host: "0.0.0.0"                    # Escuta em todas as interfaces IPv4
port: 53                           # Porta principal de escuta (DNS / Bypass DPI)
port_alt: 80                       # Porta alternativa simultânea (HTTP / Payloads)

# Segurança e Limites
token: ""                          # Token secreto opcional para ModeOpen
max_connections: 20000             # Limite máximo de túneis simultâneos
allow_private: false               # Bloquear acesso a RFC 1918 / Loopback

# Cache de Resolução DNS Interno
dns_cache_ttl: 30s                 # TTL do cache DNS interno
dns_cache_size: 4096               # Quantidade máxima de entradas de DNS

# Performance e Buffers
tcp_buffer: 0                      # 0 = Autotuning do kernel Linux
chunk_max: 1048576                 # Payload adaptativo de até 1 MiB por frame
chunk_buffered: 32                 # Buffer de download (~2 MiB por sessão)
chunk_poll_wait: 200ms             # Espera do long-poll por dados
chunk_session_timeout: 2m          # Inatividade máxima antes de fechar sessão

# Diagnósticos e Logs
debug: false                       # Logs detalhados de conexões e erros
debug_chunks: false                # Log de frames individuais (apenas depuração)
debug_stats_interval: 10s          # Intervalo de telemetria no console

# API de Administração Local (para dragontcp-cli TUI)
admin_addr: "127.0.0.1:53080"      # Restrito ao loopback local por segurança

# Fake SSH Integrado (Túnel Interno)
ssh:
  enable: true                     # Habilita o daemon Fake SSH
  listen: "127.0.0.1:2222"         # Escuta no loopback interno
  internal_host: "dragontcp-ssh.internal"  # Host interceptado para SSH
  host_key: "/etc/dragontcp/dragontcp_ssh_host_key"  # Chave privada host
  users: "/etc/dragontcp/dragontcp-users.json"       # Banco JSON de usuários

# BadVPN UDPGW Integrado (Relay UDP para Jogos & VoIP)
udpgw:
  enable: true                     # Habilita o relay UDPGW integrado
  listen: "127.0.0.1:7400"         # Escuta no loopback interno
  internal_host: "dragontcp-udpgw.internal" # Host interceptado para UDPGW
  max_clients: 10000               # Limite de clientes UDP simultâneos
  mode: "native"                   # Modo ABI Linux Nativo (SO_BINDTODEVICE e SO_BUSY_POLL)
  interface: "auto"                # Detecta a placa de rede padrão de saída
  busy_poll_us: 50                 # 50 microssegundos de polling na fila da NIC
  debug: false
EOF
        chmod 600 "${CONFIG_FILE}"
        log_success "Arquivo ${CONFIG_FILE} gerado com sucesso."
    else
        log_info "Arquivo ${CONFIG_FILE} já existente. Preservando configurações."
    fi

    # 2. Chave Privada SSH Host ED25519
    if [[ ! -f "${SSH_KEY_FILE}" ]]; then
        log_info "Gerando chave host SSH privada ED25519 em ${SSH_KEY_FILE}..."
        ssh-keygen -t ed25519 -f "${SSH_KEY_FILE}" -N "" -q
        chmod 600 "${SSH_KEY_FILE}"
        log_success "Chave SSH Host gerada com sucesso."
    fi

    # 3. Base de Usuários Fake SSH (dragontcp-users.json)
    if [[ ! -f "${USERS_FILE}" ]]; then
        log_info "Criando base inicial de usuários do Fake SSH com usuário 'admin'..."
        # Utiliza o próprio executável do servidor para criar o usuário com hash bcrypt válido
        "${SERVER_BIN}" --config "${CONFIG_FILE}" \
            --ssh-user-add "admin" \
            --ssh-user-password "DragonTCP@2026" \
            --ssh-user-days 0 \
            --ssh-user-max-connections 0 >/dev/null 2>&1 || true
        chmod 600 "${USERS_FILE}"
        log_success "Base de usuários gerada com usuário padrão: admin / DragonTCP@2026"
    fi
}

# ── Configuração de Tuning de Kernel (Sysctl) ─────────────────────────────────

apply_sysctl_tuning() {
    log_info "Aplicando tuning do kernel para alta concorrência em ${SYSCTL_FILE}..."
    cat > "${SYSCTL_FILE}" << 'EOF'
# DragonTCP Server - Otimização de Kernel para Alta Concorrência
fs.file-max = 2097152
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 15
net.ipv4.tcp_keepalive_probes = 5
EOF
    sysctl --system >/dev/null 2>&1 || sysctl -p "${SYSCTL_FILE}" >/dev/null 2>&1 || true
    log_success "Tuning de kernel aplicado com sucesso."
}

# ── Configuração do Serviço Systemd ───────────────────────────────────────────

setup_systemd_service() {
    if command -v systemctl >/dev/null 2>&1; then
        log_info "Configurando serviço systemd em ${SYSTEMD_SERVICE}..."
        cat > "${SYSTEMD_SERVICE}" << EOF
[Unit]
Description=DragonTCP High-Performance Server Daemon
Documentation=https://github.com
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${CONFIG_DIR}
ExecStart=${SERVER_BIN} --config ${CONFIG_FILE}
Restart=always
RestartSec=3
LimitNOFILE=65536
LimitNPROC=32768
StandardOutput=journal
StandardError=journal

# Permissões e capacidades de rede
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_NET_ADMIN
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable dragontcp.service
        systemctl restart dragontcp.service
        log_success "Serviço systemd habilitado e iniciado com sucesso."
    else
        log_warn "Systemd não detectado neste sistema. Inicie o servidor manualmente com:"
        echo -e "  ${WHITE}${SERVER_BIN} --config ${CONFIG_FILE}${NC}"
    fi
}

# ── Configuração de Firewall ──────────────────────────────────────────────────

setup_firewall() {
    log_info "Configurando regras de firewall para portas 53 e 80 TCP..."
    if command -v ufw >/dev/null 2>&1; then
        ufw allow 53/tcp comment 'DragonTCP Porta Principal' >/dev/null 2>&1 || true
        ufw allow 80/tcp comment 'DragonTCP Porta Alternativa' >/dev/null 2>&1 || true
        log_success "Portas 53 e 80 TCP liberadas no UFW."
    elif command -v firewall-cmd >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port=53/tcp >/dev/null 2>&1 || true
        firewall-cmd --permanent --add-port=80/tcp >/dev/null 2>&1 || true
        firewall-cmd --reload >/dev/null 2>&1 || true
        log_success "Portas 53 e 80 TCP liberadas no Firewalld."
    elif command -v iptables >/dev/null 2>&1; then
        iptables -A INPUT -p tcp --dport 53 -j ACCEPT >/dev/null 2>&1 || true
        iptables -A INPUT -p tcp --dport 80 -j ACCEPT >/dev/null 2>&1 || true
        log_success "Regras adicionadas ao IPTables para portas 53 e 80 TCP."
    else
        log_warn "Nenhum gerenciador de firewall detectado automaticamente. Verifique se as portas 53 e 80 TCP estão abertas no seu provedor de VPS."
    fi
}

# ── Ações de Controle do Serviço ──────────────────────────────────────────────

service_status() {
    print_banner
    if command -v systemctl >/dev/null 2>&1; then
        systemctl status dragontcp.service --no-pager
    else
        ps aux | grep "[d]ragontcp-server" || echo "Servidor não está em execução."
    fi
}

service_restart() {
    log_info "Reiniciando serviço DragonTCP..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl restart dragontcp.service
        log_success "Serviço reiniciado com sucesso."
    else
        pkill -f dragontcp-server || true
        nohup "${SERVER_BIN}" --config "${CONFIG_FILE}" >/dev/null 2>&1 &
        log_success "Servidor reiniciado em background."
    fi
}

service_stop() {
    log_info "Parando serviço DragonTCP..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop dragontcp.service
        log_success "Serviço parado."
    else
        pkill -f dragontcp-server || true
        log_success "Processo finalizado."
    fi
}

service_start() {
    log_info "Iniciando serviço DragonTCP..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl start dragontcp.service
        log_success "Serviço iniciado."
    else
        nohup "${SERVER_BIN}" --config "${CONFIG_FILE}" >/dev/null 2>&1 &
        log_success "Servidor iniciado em background."
    fi
}

uninstall_all() {
    print_banner
    read -rp "Tem certeza que deseja desinstalar o DragonTCP Server e CLI? (s/N): " confirm
    if [[ "$confirm" != "s" && "$confirm" != "S" ]]; then
        echo "Operação cancelada."
        exit 0
    fi

    log_info "Desinstalando DragonTCP..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop dragontcp.service 2>/dev/null || true
        systemctl disable dragontcp.service 2>/dev/null || true
        rm -f "${SYSTEMD_SERVICE}"
        systemctl daemon-reload
    fi
    pkill -f dragontcp-server 2>/dev/null || true

    rm -f "${SERVER_BIN}"
    rm -f "${CLI_BIN}"
    rm -f "${CLI_SYMLINK}"
    rm -f "${SYSCTL_FILE}"

    read -rp "Deseja também apagar as configurações e o banco de usuários (/etc/dragontcp)? (s/N): " del_data
    if [[ "$del_data" == "s" || "$del_data" == "S" ]]; then
        rm -rf "${CONFIG_DIR}"
        rm -rf "${LOG_DIR}"
        log_success "Diretório de configurações removido."
    fi

    log_success "DragonTCP desinstalado com sucesso do sistema."
}

# ── Processo de Instalação Completa ───────────────────────────────────────────

install_full() {
    print_banner
    log_info "Iniciando instalação completa do DragonTCP Server e CLI..."
    check_root
    detect_arch
    detect_distro
    install_packages
    ensure_golang
    build_binaries
    setup_config_and_files
    apply_sysctl_tuning
    setup_firewall
    setup_systemd_service

    echo ""
    echo -e "${GREEN}==============================================================================${NC}"
    echo -e "${WHITE}           🎉 DragonTCP Server e CLI Instalados com Sucesso!${NC}"
    echo -e "${GREEN}==============================================================================${NC}"
    echo -e " ${WHITE}• Binário do Servidor:${NC}  ${SERVER_BIN}"
    echo -e " ${WHITE}• Binário da CLI:${NC}       ${CLI_BIN} ${CYAN}(Atalho: dragontcp)${NC}"
    echo -e " ${WHITE}• Arquivo de Config:${NC}    ${CONFIG_FILE}"
    echo -e " ${WHITE}• Banco de Usuários:${NC}    ${USERS_FILE}"
    echo -e " ${WHITE}• Portas Abertas:${NC}       53 TCP (Principal) / 80 TCP (Secundária)"
    echo -e " ${WHITE}• Fake SSH Interno:${NC}     127.0.0.1:2222 (dragontcp-ssh.internal)"
    echo -e " ${WHITE}• UDPGW Integrado:${NC}      127.0.0.1:7400 (dragontcp-udpgw.internal)"
    echo -e " ${WHITE}• Usuário Padrão:${NC}       ${YELLOW}admin${NC} / Senha: ${YELLOW}DragonTCP@2026${NC}"
    echo -e "${BLUE}------------------------------------------------------------------------------${NC}"
    echo -e " ${CYAN}Comandos Úteis:${NC}"
    echo -e "   • Abrir Interface TUI:       ${WHITE}dragontcp${NC}"
    echo -e "   • Menu de Usuários SSH:      ${WHITE}dragontcp-server --config ${CONFIG_FILE} --ssh-menu${NC}"
    echo -e "   • Status do Serviço:         ${WHITE}systemctl status dragontcp.service${NC}"
    echo -e "   • Logs em Tempo Real:        ${WHITE}journalctl -u dragontcp.service -f${NC}"
    echo -e "${GREEN}==============================================================================${NC}"
}

# ── Menu Interativo de Execução ───────────────────────────────────────────────

menu() {
    print_banner
    echo -e " Escolha uma das opções abaixo:"
    echo ""
    echo -e "  ${WHITE}1)${NC} Instalar ou Recompilar Servidor e CLI (Instalação Completa)"
    echo -e "  ${WHITE}2)${NC} Abrir Interface Administrativa TUI (${WHITE}dragontcp${NC})"
    echo -e "  ${WHITE}3)${NC} Gerenciar Usuários SSH (${WHITE}--ssh-menu${NC})"
    echo -e "  ${WHITE}4)${NC} Status do Serviço Systemd"
    echo -e "  ${WHITE}5)${NC} Reiniciar Serviço"
    echo -e "  ${WHITE}6)${NC} Parar Serviço"
    echo -e "  ${WHITE}7)${NC} Iniciar Serviço"
    echo -e "  ${WHITE}8)${NC} Ver Logs em Tempo Real (${WHITE}journalctl${NC})"
    echo -e "  ${WHITE}9)${NC} Desinstalar DragonTCP do Servidor"
    echo -e "  ${WHITE}0)${NC} Sair"
    echo ""
    read -rp "Opção [0-9]: " opt
    case "$opt" in
        1) install_full ;;
        2)
            if [[ -x "${CLI_BIN}" ]]; then
                "${CLI_BIN}"
            else
                log_error "CLI não encontrada. Execute a instalação primeiro."
            fi
            ;;
        3)
            if [[ -x "${SERVER_BIN}" ]]; then
                "${SERVER_BIN}" --config "${CONFIG_FILE}" --ssh-menu
            else
                log_error "Servidor não encontrado. Execute a instalação primeiro."
            fi
            ;;
        4) service_status ;;
        5) service_restart ;;
        6) service_stop ;;
        7) service_start ;;
        8) journalctl -u dragontcp.service -f -o cat ;;
        9) uninstall_all ;;
        0) exit 0 ;;
        *) echo "Opção inválida."; exit 1 ;;
    esac
}

# ── Ponto de Entrada (CLI Arguments) ──────────────────────────────────────────

ACTION="${1:-}"

case "$ACTION" in
    install)
        install_full
        ;;
    update)
        check_root
        build_binaries
        service_restart
        log_success "DragonTCP atualizado com sucesso."
        ;;
    uninstall)
        check_root
        uninstall_all
        ;;
    status)
        service_status
        ;;
    restart)
        check_root
        service_restart
        ;;
    start)
        check_root
        service_start
        ;;
    stop)
        check_root
        service_stop
        ;;
    cli)
        if [[ -x "${CLI_BIN}" ]]; then
            exec "${CLI_BIN}"
        else
            log_error "CLI não instalada. Execute: $0 install"
            exit 1
        fi
        ;;
    menu|"")
        menu
        ;;
    *)
        echo "Uso: $0 [install|update|uninstall|status|restart|start|stop|cli|menu]"
        exit 1
        ;;
esac
