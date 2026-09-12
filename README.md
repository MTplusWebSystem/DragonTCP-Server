# DragonTCP Server

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go Version" />
  <img src="https://img.shields.io/badge/Protocol-Multiplexed%20v2%20(33%2F9)-7928CA?style=for-the-badge" alt="Protocol" />
  <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20Windows-0078D6?style=for-the-badge" alt="Platform" />
  <img src="https://img.shields.io/badge/License-Proprietary-red?style=for-the-badge" alt="License" />
</p>

O **DragonTCP Server** é um servidor de encapsulamento e transporte TCP de ultra-alto desempenho, desenvolvido em Go e projetado especificamente para operar com resiliência, baixa latência e evasão em redes restritas, ambientes com inspeção profunda de pacotes (DPI) e cenários com alta concorrência de conexões.

O projeto conta com arquitetura híbrida inteligente: clientes legados (**v1 Stop-and-Wait**) e modernos (**v2 Multiplexado**) coexistem harmonicamente na mesma porta sem qualquer conflito, acompanhados de serviços internos de **Fake SSH** (com isolamento do sistema operacional), **BadVPN UDPGW** (com descarga direta na placa de rede via Linux ABI) e **CLI TUI** de gerenciamento em tempo real.

---

## ⚡ Início Rápido (Quick Start)

Para instalar o servidor e a CLI administrativa em qualquer servidor Linux (Ubuntu, Debian, CentOS, AlmaLinux, Rocky, Alpine ou Arch):

```bash
# Executar o script instalador oficial automatizado:
chmod +x install.sh
sudo ./install.sh install
```

Após a instalação, abra o painel administrativo interativo:
```bash
dragontcp
```

---

## 📚 Sumário

- [Visão Geral e Novidades da v2](#-visão-geral-e-novidades-da-v2)
- [Comparativo: Protocolo v1 vs. Protocolo v2](#-comparativo-protocolo-v1-vs-protocolo-v2)
- [Arquitetura e Fluxo Multiplexado](#-arquitetura-e-fluxo-multiplexado)
- [Recursos Principais](#-recursos-principais)
- [Especificação de Cabeçalhos (Wire Format)](#-especificação-de-cabeçalhos-wire-format)
- [Compatibilidade Híbrida e Sniffing Binário](#-compatibilidade-híbrida-e-sniffing-binário)
- [BadVPN UDPGW com ABI Linux Nativo](#-badvpn-udpgw-com-abi-linux-nativo)
- [Serviço Fake SSH Integrado](#-serviço-fake-ssh-integrado)
- [Interface Administrativa TUI (DragonTCP CLI)](#-interface-administrativa-tui-dragontcp-cli)
- [Guia de Configuração (YAML e CLI)](#-guia-de-configuração-yaml-e-cli)
- [Instalação e Systemd](#-instalação-e-systemd)
- [Destaques de Performance e Benchmarks](#-destaques-de-performance-e-benchmarks)
- [Central de Documentação Oficial (docs/)](#-central-de-documentação-oficial-docs)
- [Compilação e Testes](#-compilação-e-testes)
- [Licença](#-licença)

---

## 🚀 Visão Geral e Novidades da v2

A versão 2 do DragonTCP Server introduz o **Protocolo Binário Multiplexado v2 (33/9 bytes)**, reformulando por completo o modelo de transporte entre cliente e servidor:

1. **Multiplexação Real em 1 Conexão Física**: Uma única conexão física TCP agora transporta concorrentemente dados de dezenas ou centenas de sessões lógicas sem necessidade de novos *handshakes*.
2. **Eliminação do Gargalo Stop-and-Wait**: O protocolo v1 exigia que cada requisição-resposta fosse síncrona. Na v2, requisições recebem um identificador único de 32 bits (`RequestID`), permitindo despacho assíncrono e intercalação fluida de pacotes no socket.
3. **Serialização Atômica (`writeMu`)**: Mecanismo interno com mutex dedicado que garante que múltiplos frames de sessões distintas nunca se sobreponham ou corrompam o fluxo binário no socket físico.
4. **Economia Expressiva de Recursos**: Queda de até **90% no uso de file descriptors (FDs)** e eliminação do overhead de dezenas de handshakes TCP concorrentes.
5. **Descarga UDP via ABI Linux**: Módulo UDPGW reescrito com `SO_BINDTODEVICE` e `SO_BUSY_POLL` na interface física, reduzindo a latência de jogos e chamadas de voz para menos de 1 ms.
6. **Interface de Terminal Moderna (TUI)**: Ferramenta gráfica de terminal (`dragontcp-cli`) com telemetria de tráfego, gestão de contas e corte forçado de sessões (*kill connection*).

---

## 📊 Comparativo: Protocolo v1 vs. Protocolo v2

| Aspecto | Protocolo v1 (Legado) | Protocolo v2 (Multiplexado) | Vantagem v2 |
| :--- | :--- | :--- | :--- |
| **Cabeçalho Requisição** | **29 bytes fixos** | **33 bytes fixos** | Campo `RequestID` de 4 bytes |
| **Formato Requisição** | `[mode: 1B][SessionID: 16B][seq: 8B][len: 4B]` | `[mode: 1B][SessionID: 16B][seq: 8B][req_id: 4B][len: 4B]` | Identificação unívoca por requisição |
| **Cabeçalho Resposta** | **5 bytes fixos** | **9 bytes fixos** | Campo `RequestID` de 4 bytes |
| **Formato Resposta** | `[status: 1B][len: 4B]` | `[status: 1B][req_id: 4B][len: 4B]` | Respostas assíncronas correlacionáveis |
| **Modelo de Transporte** | Stop-and-Wait / N sockets físicos | Multiplexação assíncrona em 1 socket | Elimina latência de handshakes |
| **Concorrência** | Serial por conexão TCP | Paralela via goroutines assíncronas | Maior vazão agregada |
| **Uso de Portas/Sockets** | Alto (1 socket por fluxo ativo) | Mínimo (dezenas de túneis por socket) | Reduz pressão no kernel e firewall |
| **Resistência a DPI** | Suscetível a exaustão de conexões | Canal contínuo e persistente | Dificulta identificação por firewalls |

---

## 🧩 Arquitetura e Fluxo Multiplexado

No protocolo v2, o servidor mantém um loop central de leitura (`dispatchLoop`) na conexão TCP física que lê os frames de 33 bytes de forma sequencial e dispara o processamento de cada requisição em uma goroutine assíncrona independente.

As respostas geradas pelas diferentes goroutines são enviadas de volta ao cliente passando pelo `muxServerConn`, onde um mutex de escrita (`writeMu`) garante a integridade de cada frame no socket físico.

```text
       CLIENTE                                            SERVIDOR (DragonTCP v2)
          |                                                          |
          |=== [ReqID: 1, Sessão A, Seq 0] (ModeOpen) ==============>| (Goroutine 1 - Abre Sessão A)
          |=== [ReqID: 2, Sessão B, Seq 0] (ModeOpen) ==============>| (Goroutine 2 - Abre Sessão B)
          |=== [ReqID: 3, Sessão A, Seq 0] (ModeUpload 1KiB) =======>| (Goroutine 3 - Upload Sessão A)
          |                                                          |
          |<== [Resp ReqID: 2, StatusOK] (Sessão B Aberta) ==========| (Processamento rápido da Sessão B)
          |<== [Resp ReqID: 1, StatusOK] (Sessão A Aberta) ==========| (Conclusão do dial da Sessão A)
          |<== [Resp ReqID: 3, StatusOK] (Upload A Concluído) =======| (ACK de upload)
          |                                                          |
          |=== [ReqID: 4, Sessão A, Seq 0] (ModeDownload) ==========>|
          |                                                          |
          |<== [Resp ReqID: 4, StatusData, 1024B] ===================| (Streaming de dados Sessão A)
          |<== [Resp ReqID: 4, StatusWait] ==========================| (Término do lote de download)
```

---

## ✨ Recursos Principais

* **Multiplexador v2 de Alta Concorrência**: Tratamento assíncrono para todos os modos (`ModeProbe`, `ModeOpen`, `ModeUpload`, `ModeDownload`, `ModeClose`), com eco obrigatório de `RequestID`.
* **Compatibilidade Híbrida Automática**: Roteamento dinâmico sem necessidade de portas separadas:
  * **v1**: Headers clássicos de 29/5 bytes continuam funcionando perfeitamente.
  * **v2**: Headers de 33/9 bytes identificados via detecção no preface (`cover.Profile.MuxV2`) ou via sniffing binário inteligente.
  * **XOR (UP/OK)**: Suporte herdado e interoperável com clientes LiteVPN v4.
  * **HTTP / WebSocket**: Handshake automático `101 Switching Protocols` para bypass de operadoras com payloads zero-rated.
* **Ofuscação Criptográfica & Proteção DPI**:
  * Máscaras variáveis de primeiro byte (`headerMask`).
  * Preface de cobertura com padding criptograficamente aleatório (`cover.Profile`).
  * Mascaramento de payload por sessão derivado via SHA-256 (`MaskInPlace`), com throughput ultra-rápido (>9.5 GB/s em modo claro).
* **Fake SSH Integrado**:
  * Daemon SSH interno executado em loopback (`127.0.0.1:2222`) e interceptado internamente por nome de host (`dragontcp-ssh.internal`).
  * Banco de usuários isolado em JSON com hash `bcrypt`.
  * Controle de validade em dias e limite de conexões simultâneas por usuário.
  * Menu interativo via terminal (`--ssh-menu`) e subcomandos de automação CLI.
* **BadVPN UDPGW Integrado de Baixa Latência**:
  * Modo **`native`** com chamadas diretas de Linux ABI: `SO_BINDTODEVICE`, `SO_BUSY_POLL` e buffers de 4 MB para jogos e chamadas de voz estáveis.
* **Interface de Terminal (TUI) & API Administrativa**:
  * Endpoint REST local (`127.0.0.1:53080`) com monitoramento de métricas, conexões ativas e encerramento forçado de túneis.
  * CLI visual moderna construída em Charmbracelet Bubble Tea / Lipgloss (`dragontcp-cli`).

---

## 📦 Especificação de Cabeçalhos (Wire Format)

### Requisição v2 (33 bytes)

```text
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|  Mode ^ Mask  |                                               |
+-+-+-+-+-+-+-+-+                                               +
|                                                               |
+                      Session ID (16 bytes)                    +
|                                                               |
+               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|               |                 Sequence (8 bytes)            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                               |          Request ID           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|          Request ID           |        Payload Length         |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|        Payload Length         |    Payload (Length bytes)...  |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### Resposta v2 (9 bytes)

```text
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Status ^ Mask |                  Request ID                   |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|          Request ID           |          Body Length          |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|          Body Length          |      Body (Length bytes)...   |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### Modos e Status Operacionais

| Modo | Valor | Descrição |
| :--- | :---: | :--- |
| `ModeProbe` | `0x00` | Sondagem de conectividade, calibração de MTU e latência |
| `ModeOpen` | `0x01` | Abertura de túnel TCP para `host:port` com autenticação por token |
| `ModeUpload` | `0x02` | Envio sequencial de dados do cliente para o destino |
| `ModeDownload`| `0x03` | Solicitação em lote de download de dados do destino |
| `ModeClose` | `0x04` | Fechamento explícito da sessão lógica e liberação imediata de recursos |

| Status | Valor | Descrição |
| :--- | :---: | :--- |
| `StatusOK` | `0x00` | Operação concluída com sucesso |
| `StatusError` | `0x01` | Erro na operação (corpo contém a mensagem em texto claro) |
| `StatusData` | `0x02` | Frame contendo fatia de dados recebida do alvo |
| `StatusWait` | `0x03` | Lote consumido sem mais dados imediatos no buffer |
| `StatusEOF` | `0x04` | Conexão remota com o destino foi encerrada |

---

## 🔍 Compatibilidade Híbrida e Sniffing Binário

O servidor utiliza uma máquina de estados de baixíssima sobrecarga (`sniffWire`) para classificar conexões de forma não-destrutiva via `bufio.Reader.Peek`:

1. **Cover Preface (`cover.Profile`)**: Inspeciona a assinatura de 12 bytes (`DTC3`), extrai a máscara e lê a capacidade multiplexada v2.
2. **XOR Wire (UP/OK)**: Detecta cabeçalhos legados do protocolo XOR e despacha para `handleXOR`.
3. **Payloads HTTP / WebSocket**: Identifica requisições HTTP normais ou upgrades WebSocket, responde com `HTTP/1.1 101 Switching Protocols` e faz upgrade instantâneo para o túnel Mux v2.
4. **Conexões Binárias Diretas**: Distingue entre frames v1 (29B) e v2 (33B) analisando offsets de `ProbeMagic` (`DTP2`) e comprimentos de campos sem bloquear e sem descartar nenhum byte.

---

## 🎮 BadVPN UDPGW com ABI Linux Nativo

O UDPGW embutido no DragonTCP elimina a necessidade de instalar pacotes externos do BadVPN e traz três modos operacionais configuráveis no YAML (`udpgw.mode`):

* **`native` (Padrão Linux):** Utiliza `SO_BINDTODEVICE` para descarregar os datagramas UDP diretamente na interface física (ex.: `eth0`), ignorando a tabela de rotas do kernel, e ativa `SO_BUSY_POLL` (polling ativo de 50 µs na fila da NIC), reduzindo o jitter e mantendo latência inferior a 1 ms para Free Fire, PUBG e chamadas de voz.
* **`tun`:** Injeta pacotes diretamente no dispositivo `/dev/net/tun` do sistema operacional.
* **`standard`:** Modo portável user-space compatível com qualquer sistema operacional (Linux, Windows, macOS).

---

## 🔑 Serviço Fake SSH Integrado

O Fake SSH embutido roda em loopback (`127.0.0.1:2222`) e só pode ser acessado através de túneis abertos para o host reservado `dragontcp-ssh.internal`.

### Gerenciamento Rápido via Terminal:

```bash
# Adicionar usuário com validade de 30 dias e máximo de 2 conexões:
dragontcp-server --config /etc/dragontcp/dragontcp.yaml \
  --ssh-user-add "cliente1" \
  --ssh-user-password "Senha@2026" \
  --ssh-user-days 30 \
  --ssh-user-max-connections 2

# Listar usuários e status de expiração:
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-list

# Excluir usuário:
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-delete "cliente1"

# Menu interativo assistido:
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-menu
```

---

## 🖥️ Interface Administrativa TUI (DragonTCP CLI)

A CLI interativa (`dragontcp-cli` ou atalho `dragontcp`) comunica-se via API REST local com o servidor:

```bash
dragontcp
```

### Funcionalidades da TUI:
* **Monitoramento em Tempo Real**: Telemetria de tráfego (Up/Down em MiB/GiB), conexões ativas e sessões lógicas.
* **Métricas do Runtime Go**: Goroutines ativas, consumo de RAM alocada e ciclos do Garbage Collector.
* **Gestão Visual de Usuários**: Criação, exclusão e bloqueio de usuários do Fake SSH com formulários assistidos.
* **Monitor de Conexões e "Kill Session"**: Visualize cada túnel aberto, destino remoto e buffer em retenção, com opção de encerrar qualquer conexão imediatamente.
* **Editor YAML Embutido**: Edição direta das diretivas de configuração com validação e salvamento no disco.

---

## ⚙️ Guia de Configuração (YAML e CLI)

O DragonTCP adota uma ordem estrita de precedência:
```text
Flags de Linha de Comando  >  Arquivo YAML (--config)  >  Valores Padrão Internos
```

### Exemplo de Configuração de Produção (`/etc/dragontcp/dragontcp.yaml`):

```yaml
# Rede e Escuta
host: "0.0.0.0"                    # Interface de escuta
port: 53                           # Porta principal (DNS / Bypass DPI)
port_alt: 80                       # Porta secundária (HTTP / Payloads)

# Segurança e Limites
token: ""                          # Token secreto de autenticação opcional
max_connections: 20000             # Limite máximo de túneis simultâneos
allow_private: false               # Bloquear acesso a RFC 1918 / Loopback

# Performance e Buffers
dns_cache_ttl: 30s                 # TTL do cache DNS interno
dns_cache_size: 4096               # Quantidade de entradas no cache DNS
tcp_buffer: 0                      # 0 = Autotuning do kernel Linux
chunk_max: 1048576                 # Payload adaptativo de até 1 MiB
chunk_buffered: 32                 # Buffer de download (~2 MiB por sessão)
chunk_poll_wait: 200ms             # Espera do long-poll por dados
chunk_session_timeout: 2m          # Inatividade máxima antes de fechar sessão

# Diagnósticos e Logs
debug: false                       # Logs operacionais detalhados
debug_chunks: false                # Log de cada frame individual (apenas testes)
debug_stats_interval: 10s          # Intervalo de telemetria no console

# API de Administração Local (para a TUI)
admin_addr: "127.0.0.1:53080"

# Fake SSH Integrado
ssh:
  enable: true
  listen: "127.0.0.1:2222"
  internal_host: "dragontcp-ssh.internal"
  host_key: "/etc/dragontcp/dragontcp_ssh_host_key"
  users: "/etc/dragontcp/dragontcp-users.json"

# BadVPN UDPGW Integrado
udpgw:
  enable: true
  listen: "127.0.0.1:7400"
  internal_host: "dragontcp-udpgw.internal"
  max_clients: 10000
  mode: "native"                   # ABI Linux: SO_BINDTODEVICE e SO_BUSY_POLL
  interface: "auto"                # Detecta placa física de saída (eth0, ens3)
  busy_poll_us: 50                 # 50 µs de polling na fila da NIC
  debug: false
```

---

## 🐧 Instalação e Systemd

### Instalação Automatizada (Recomendado)
Execute o script instalador oficial:
```bash
chmod +x install.sh
sudo ./install.sh install
```

### Instalação Manual Passo a Passo:
1. Compile os binários de produção:
   ```bash
   go build -ldflags="-s -w" -o /usr/local/bin/dragontcp-server .
   go build -ldflags="-s -w" -o /usr/local/bin/dragontcp-cli ./cli
   ln -sf /usr/local/bin/dragontcp-cli /usr/local/bin/dragontcp
   ```
2. Crie os diretórios e copie a configuração:
   ```bash
   mkdir -p /etc/dragontcp /var/log/dragontcp
   cp dragontcp.example.yaml /etc/dragontcp/dragontcp.yaml
   ```
3. Crie a unit do serviço em `/etc/systemd/system/dragontcp.service`:
   ```ini
   [Unit]
   Description=DragonTCP Server Daemon
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

   [Install]
   WantedBy=multi-user.target
   ```
4. Ative e inicie o daemon:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dragontcp.service
   systemctl status dragontcp.service
   ```

---

## ⚡ Destaques de Performance e Benchmarks

Resultados reais obtidos com o compilador Go nos micro-benchmarks do pacote `internal/wire`:

* **Criptografia SHA-256 In-Place (`BenchmarkMask1MiB`)**: **~280 MB/s** por núcleo de CPU com zero alocações adicionais.
* **Modo Claro / Clear Payload (`BenchmarkWriteRequest1MiB/clear`)**: **>9.5 GB/s** (109.5 ns/op), aproveitando a criptografia nativa de protocolos superiores (como TLS 1.3 ou SSH).
* **Concorrência com 64 Workers (`TestMuxParallelWorkersIperf`)**: Zero deadlocks no mutex `writeMu` e zero erros de transmissão.
* **UDPGW em Modo Nativo**: Redução de ~85% na latência e zero descarte de datagramas em rajadas graças a buffers forçados de 4 MB.

Para análise completa de métricas, gráficos e comandos de reprodução, consulte o [Relatório de Benchmarks](docs/benchmarks.md).

---

## 📚 Central de Documentação Oficial (`docs/`)

O repositório conta com uma central de documentação completa e detalhada:

| Documento | Descrição Principal |
| :--- | :--- |
| 📖 **[Guia de Parâmetros](docs/parameters.md)** | Especificação exaustiva de todas as flags CLI, opções YAML e variáveis de kernel. |
| ⚡ **[Relatório de Benchmarks](docs/benchmarks.md)** | Metodologia de testes, comparativos v1 vs v2, gráficos e comandos de benchmark. |
| 🚀 **[Guia de Instalação](docs/installation.md)** | Manual detalhado de compilação, requisitos, segurança e configuração de firewall. |
| 🤖 **[Script Instalador (`install.sh`)](install.sh)** | Script bash oficial de automação completa para Linux. |
| 💡 **[Pasta de Exemplos (`docs/ex/`)](docs/ex/README.md)** | Cenários práticos prontos para implantação: |
| &nbsp;&nbsp;&nbsp;&nbsp;↳ [Servidor de Produção](docs/ex/producao.md) | Configuração VPS de alto tráfego para 20.000 conexões com systemd e sysctl. |
| &nbsp;&nbsp;&nbsp;&nbsp;↳ [Gestão do Fake SSH](docs/ex/fake_ssh.md) | Criação de usuários em lote, expiração, limites de conexões e menu interativo. |
| &nbsp;&nbsp;&nbsp;&nbsp;↳ [UDPGW para Jogos e VoIP](docs/ex/udpgw_gaming.md) | Calibração do modo ABI Linux (`SO_BUSY_POLL`) para jogos de baixa latência. |
| &nbsp;&nbsp;&nbsp;&nbsp;↳ [Payloads HTTP e Bypass DPI](docs/ex/http_payloads.md) | Handshake WebSocket 101 e evasão em redes móveis com domínios zero-rated. |
| &nbsp;&nbsp;&nbsp;&nbsp;↳ [Operação via CLI TUI](docs/ex/cli_tui.md) | Manual de uso do painel interativo no terminal (`dragontcp-cli`). |

---

## 🛠️ Compilação e Testes

### Compilar Binários de Produção

```bash
# Servidor
go build -ldflags="-s -w" -o dragontcp-server .

# CLI Administrativa
go build -ldflags="-s -w" -o dragontcp-cli ./cli
```

### Executar a Suíte de Testes Automatizados

```bash
# Executar todos os testes do projeto
go test -v ./...

# Executar testes com o detector de condições de corrida (Race Detector)
go test -race -run TestMux .

# Executar os micro-benchmarks do protocolo wire
go test -bench=. -benchmem -run=^$ ./internal/wire
```

---

## 📄 Licença

Software proprietário. Todos os direitos reservados. Proibida redistribuição ou uso não autorizado.
