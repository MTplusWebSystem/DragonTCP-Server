# DragonTCP Server

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Protocol](https://img.shields.io/badge/Protocol-Multiplexed%20v2%20(33%2F9)-blueviolet)](https://github.com)
[![License](https://img.shields.io/badge/License-Proprietary-red)](#)

O **DragonTCP Server** é um servidor de encapsulamento e transporte TCP de alta performance, projetado para operar com eficiência e resiliência em redes restritas, ambientes com inspeção profunda de pacotes (DPI) e cenários com alta concorrência de conexões.

O projeto conta com arquitetura híbrida inteligente, permitindo que clientes legados (**v1**) e clientes atualizados (**v2 Multiplexado**) coexistam na mesma porta sem conflitos, além de suportar tráfego ofuscado (**XOR / cover prefaces**) e serviços internos de **Fake SSH** e **UDPGW** integrados.

---

## Sumário

- [Visão Geral e Novidades da v2](#-visão-geral-e-novidades-da-v2)
- [Comparativo: Protocolo v1 vs. Protocolo v2](#-comparativo-protocolo-v1-vs-protocolo-v2)
- [Arquitetura e Fluxo Multiplexado](#-arquitetura-e-fluxo-multiplexado)
- [Recursos Principais](#-recursos-principais)
- [Especificação de Cabeçalhos](#-especificação-de-cabeçalhos)
- [Compatibilidade Híbrida e Sniffing](#-compatibilidade-híbrida-e-sniffing)
- [Guia de Configuração (YAML e CLI)](#-guia-de-configuração-yaml-e-cli)
- [Instalação em Produção (Systemd)](#-instalação-em-produção-systemd)
- [Gerenciamento do Fake SSH](#-gerenciamento-do-fake-ssh)
- [Compilação e Testes](#-compilação-e-testes)
- [Documentação Oficial (Pasta docs/)](#-documentação-oficial-pasta-docs)

---

## 🚀 Visão Geral e Novidades da v2

A versão 2 do DragonTCP Server introduz o **Protocolo Binário Multiplexado v2 (33/9 bytes)**, reformulando o modelo de transporte entre cliente e servidor:

1. **Multiplexação Real em 1 Conexão Física**: Uma única conexão física TCP agora transporta concorrentemente dados de dezenas ou centenas de sessões lógicas.
2. **Eliminação do Gargalo Stop-and-Wait**: O protocolo v1 exigia que cada par requisição-resposta fosse síncrono ou demandasse uma nova conexão TCP. Na v2, requisições recebem um identificador único de 32 bits (`RequestID`), permitindo despacho assíncrono e intercalação de pacotes no socket.
3. **Serialização Atômica (`writeMu`)**: Mecanismo interno com mutex dedicado que garante que múltiplos frames de sessões distintas nunca se sobreponham ou corrompam o fluxo binário no socket.
4. **Economia Expressiva de Recursos**: Queda de até 90% no uso de file descriptors (FDs) e overhead de handshakes TCP no sistema operacional do servidor.
5. **Configuração Declarativa em Produção**: Suporte a `--config arquivo.yaml` com suporte a chaves kebab-case, snake_case e blocos aninhados, integrando perfeitamente com `systemd`.

---

## 📊 Comparativo: Protocolo v1 vs. Protocolo v2

| Aspecto | Protocolo v1 (Legado) | Protocolo v2 (Multiplexado) | Vantagem v2 |
| :--- | :--- | :--- | :--- |
| **Cabeçalho de Requisição** | **29 bytes fixos** | **33 bytes fixos** | Inclui campo `request_id` (4 bytes) |
| **Formato Requisição** | `[mode: 1B][SessionID: 16B][seq: 8B][len: 4B]` | `[mode: 1B][SessionID: 16B][seq: 8B][request_id: 4B][len: 4B]` | Identificação unívoca por requisição |
| **Cabeçalho de Resposta** | **5 bytes fixos** | **9 bytes fixos** | Inclui campo `request_id` (4 bytes) |
| **Formato Resposta** | `[status: 1B][len: 4B]` | `[status: 1B][request_id: 4B][len: 4B]` | Respostas assíncronas correlacionáveis |
| **Modelo de Transporte** | Stop-and-Wait ou N conexões físicas | Multiplexação assíncrona em 1 conexão física | Elimina latência de handshakes repetidos |
| **Concorrência** | Serial por conexão TCP | Paralela via goroutines assíncronas | Maior vazão (throughput) agregada |
| **Uso de Portas/Sockets** | Alto (1 socket por fluxo ativo) | Mínimo (dezenas de sessões por socket) | Reduz pressão no kernel e firewall |
| **Resistência a Bloqueios** | Suscetível a exaustão de conexões | Canal contínuo e persistente | Dificulta identificação por DPI |

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

- **Multiplexador v2 Robusto**: Tratamento completo para todos os modos operacionais (`ModeProbe`, `ModeOpen`, `ModeUpload`, `ModeDownload`, `ModeClose`), com eco obrigatório de `RequestID`.
- **Compatibilidade Híbrida Automática**: Roteamento dinâmico sem necessidade de portas separadas:
  - **v1**: Headers de 29/5 bytes continuam funcionando perfeitamente.
  - **v2**: Headers de 33/9 bytes identificados via detecção no preface (`cover.Profile.MuxV2`) ou via sniffing binário inteligente.
  - **XOR (UP/OK)**: Suporte herdado e interoperável com clientes LiteVPN v4.
- **Ofuscação e Proteção DPI**:
  - Máscaras variáveis de primeiro byte (`headerMask`).
  - Preface de cobertura com padding criptograficamente aleatório (`cover.Profile`).
  - Mascaramento de payload por sessão derivado via SHA-256 (`MaskInPlace`), com suporte a modo claro (`ClearPayload`) quando sinalizado no preface.
- **Fake SSH Integrado**:
  - Servidor SSH interno executado em loopback (`127.0.0.1:2222` por padrão) ou endereço dedicado.
  - Banco de usuários em JSON com hash de senha via `bcrypt`.
  - Limite de conexões simultâneas por conta e controle de expiração em dias.
  - Menu interativo via CLI (`--ssh-menu`) e subcomandos de automação (`--ssh-user-add`, `--ssh-user-delete`, `--ssh-user-list`).
- **BadVPN UDPGW Integrado**:
  - Relay de pacotes UDP compatível com BadVPN/UDPGW (`127.0.0.1:7400`).
  - Permite suporte a jogos online, chamadas de voz e tráfego UDP em clientes tunelados.
- **Configuração Flexível (CLI & YAML)**:
  - Carregamento de arquivo YAML via `--config <caminho.yaml>`.
  - Ordem estrita de precedência: **Flags de Linha de Comando > Arquivo YAML > Valores Padrão**.

---

## 📦 Especificação de Cabeçalhos

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
| `ModeClose` | `0x04` | Fechamento explícito da sessão lógica e liberação de sockets |

| Status | Valor | Descrição |
| :--- | :---: | :--- |
| `StatusOK` | `0x00` | Operação concluída com sucesso |
| `StatusError` | `0x01` | Erro na operação (corpo contém a mensagem de texto do erro) |
| `StatusData` | `0x02` | Frame contendo fatia de dados recebida do alvo |
| `StatusWait` | `0x03` | Lote consumido sem mais dados imediatos no buffer |
| `StatusEOF` | `0x04` | Conexão remota com o destino foi encerrada |

---

## 🔍 Compatibilidade Híbrida e Sniffing

O servidor utiliza uma máquina de estados de baixa sobrecarga na função `sniffWire` para classificar o tráfego que chega na porta:

1. **Cover Preface (`cover.Profile`)**:
   - Os primeiros 12 bytes são verificados contra a assinatura do preface `DTC3`.
   - Se for um preface válido, extrai o `HeaderMask`, descarta o padding aleatório e lê a flag de capacidade `MuxV2` (bit 2). Se ativo, encaminha imediatamente para o manipulador multiplexado v2.
2. **XOR Wire (UP/OK)**:
   - Se o primeiro byte indicar o cabeçalho clássico do protocolo XOR (`UP`), a conexão é roteada para `handleXOR`.
3. **Conexões Binárias Diretas**:
   - Para conexões sem preface, o sniffer inspeciona o primeiro registro de forma não-destrutiva via `bufio.Reader.Peek`:
     - Em `ModeProbe`: valida a presença de `ProbeMagic` (`DTP2`) no offset 29 (v1) ou offset 33 (v2).
     - Em `ModeOpen`: analisa a correlação entre o comprimento do payload e os campos `tl + hl + 6` no offset 29 (v1) versus offset 33 (v2).
     - Em `ModeDownload`: diferencia o tamanho fixo de 14 bytes entre `seq`/`len` e `request_id`.
   - Garante 100% de precisão sem bloquear e sem descartar nenhum byte.

---

## ⚙️ Guia de Configuração (YAML e CLI)

O servidor pode ser configurado inteiramente através de um arquivo YAML ou via flags tradicionais de linha de comando.

### Arquivo de Configuração (`dragontcp.yaml`)

Crie o arquivo `/etc/dragontcp/dragontcp.yaml` com base no modelo:

```yaml
# ==============================================================================
# DragonTCP Server - Arquivo de Configuração de Produção
# ==============================================================================

# Rede e Escuta
host: "0.0.0.0"                    # Interface de escuta (0.0.0.0 para todas)
port: 53                           # Porta principal de escuta (DNS / Porta 53 recomendada)
port_alt: 80                       # Porta secundária simultânea (0 para desativar)

# Segurança e Limites
token: ""                          # Token compartilhado opcional de autorização
max_connections: 20000             # Limite máximo de túneis simultâneos
allow_private: false               # Permitir destinos privados/loopback (RFC 1918)

# Cache de Resolução DNS Interno
dns_cache_ttl: 30s                 # Tempo de vida (TTL) do cache DNS
dns_cache_size: 4096               # Quantidade máxima de hostnames em cache

# Performance e Buffers
tcp_buffer: 0                      # Buffer TCP em bytes (0 = autotuning do kernel Linux)
chunk_max: 1048576                 # Tamanho máximo de chunk de payload (32 B até 1 MiB)
chunk_buffered: 32                 # Buffer de download por sessão em unidades de 64 KiB (~2 MiB)
chunk_poll_wait: 200ms             # Espera do long-poll do servidor por novos dados
chunk_session_timeout: 2m          # Tempo de inatividade antes de encerrar sessão ociosa

# Diagnósticos e Logs
debug: false                       # Ativar logs de conexões, erros e estatísticas
debug_chunks: false                # Logar cada registro binário trafegado (muito verboso)
debug_stats_interval: 5s           # Intervalo de estatísticas periódicas (0 para desativar)

# Fake SSH Interno (Túnel Integrado)
ssh:
  enable: true                     # Habilitar o serviço interno de Fake SSH
  listen: "127.0.0.1:2222"         # Endereço de escuta do Fake SSH
  internal_host: "dragontcp-ssh.internal"  # Host interceptado para conexões SSH
  host_key: "dragontcp_ssh_host_key"       # Caminho da chave privada ed25519 do servidor
  users: "dragontcp-users.json"            # Caminho da base de usuários JSON

# BadVPN UDPGW Integrado
udpgw:
  enable: true                     # Habilitar o relay UDPGW
  listen: "127.0.0.1:7400"         # Endereço de escuta do UDPGW (loopback recomendado)
  internal_host: "dragontcp-udpgw.internal" # Host interceptado para UDPGW
  max_clients: 10000               # Limite de clientes UDP simultâneos
  debug: false                     # Logs detalhados de erros UDP
```

### Regras de Precedência

```text
Flags CLI (--port, --debug)  >  Arquivo YAML (--config)  >  Valores Padrão Internos
```

Exemplo: se o arquivo `dragontcp.yaml` definir `port: 53`, mas o servidor for iniciado com `--port 443`, a porta **443** será utilizada.

---

## 🐧 Instalação em Produção (Systemd)

Graças ao suporte ao arquivo `--config`, a unit do `systemd` fica extremamente limpa e simples de manter.

1. Compile e mova o binário para o diretório de sistema:
   ```bash
   go build -o /usr/local/bin/dragontcp-server .
   chmod +x /usr/local/bin/dragontcp-server
   ```

2. Crie o diretório de configuração e copie o arquivo:
   ```bash
   mkdir -p /etc/dragontcp
   cp dragontcp.example.yaml /etc/dragontcp/dragontcp.yaml
   ```

3. Crie a unit do serviço em `/etc/systemd/system/dragontcp.service`:
   ```ini
   [Unit]
   Description=DragonTCP Server Daemon
   After=network.target

   [Service]
   Type=simple
   User=root
   LimitNOFILE=65536
   ExecStart=/usr/local/bin/dragontcp-server --config /etc/dragontcp/dragontcp.yaml
   Restart=always
   RestartSec=3

   [Install]
   WantedBy=multi-user.target
   ```

4. Habilite e inicie o serviço:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dragontcp.service
   systemctl status dragontcp.service
   ```

---

## 🔑 Gerenciamento do Fake SSH

O Fake SSH integrado aceita autenticação via usuário e senha, isolando as credenciais de sistema do servidor real.

### Comandos de Gerenciamento

- **Menu Interativo**:
  ```bash
  dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-menu
  ```
- **Adicionar ou Atualizar Usuário**:
  ```bash
  dragontcp-server --config /etc/dragontcp/dragontcp.yaml \
    --ssh-user-add usuario1 \
    --ssh-user-password "SenhaForte123!" \
    --ssh-user-days 30 \
    --ssh-user-max-connections 2
  ```
- **Remover Usuário**:
  ```bash
  dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-delete usuario1
  ```
- **Listar Usuários Ativos**:
  ```bash
  dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-list
  ```

> [!TIP]
> Ao usar `--config`, o utilitário obtém automaticamente o caminho correto do arquivo `ssh-users` configurado no YAML, dispensando flags extras.

---

## 🛠️ Compilação e Testes

### Compilação do Binário

```bash
# Compilar binário de produção
go build -ldflags="-s -w" -o dragontcp-server .
```

### Executar Testes Automatizados

O repositório inclui suíte abrangente cobrindo todas as camadas do sistema:

```bash
# Executar todos os testes
go test -v ./...

# Executar testes específicos de multiplexação e concorrência
go test -v -run TestMux .

# Executar testes com o detector de condições de corrida (Race Detector)
go test -race -run TestMux .

# Executar suíte completa sem cache
go test -count=1 ./...
```

---

## 📚 Documentação Oficial (Pasta `docs/`)

Para detalhes avançados, guias de implantação e especificações técnicas completas, consulte a documentação dedicada na pasta [`docs/`](docs/):

- 📖 **[Guia Completo de Parâmetros](docs/parameters.md)**: Referência exaustiva de todas as flags CLI do servidor e TUI, diretivas YAML (formatos aninhado e plano, *kebab-case* e *snake_case*), limites de conexão e opções de kernel.
- 💡 **[Exemplos Práticos (`docs/ex/`)](docs/ex/README.md)**:
  - [Servidor de Produção](docs/ex/producao.md): VPS Linux de alto tráfego com systemd e sysctl.
  - [Gestão do Fake SSH](docs/ex/fake_ssh.md): Criação de contas, controle de validade e conexões simultâneas.
  - [UDPGW para Jogos e VoIP](docs/ex/udpgw_gaming.md): Descarga direta na placa de rede via Linux ABI com `SO_BUSY_POLL`.
  - [Payloads HTTP e WebSocket](docs/ex/http_payloads.md): Handshake 101 Switching Protocols e bypass de DPI.
  - [Operação com a CLI TUI](docs/ex/cli_tui.md): Painel visual interativo no terminal (`dragontcp-cli`).
- ⚡ **[Relatório de Benchmarks](docs/benchmarks.md)**: Resultados de testes de estresse, micro-benchmarks do Go (`internal/wire`), comparativos v1 vs v2 e métricas de latência.
- 🚀 **[Guia de Instalação e Implantação](docs/installation.md)**: Procedimentos manuais e automatizados, requisitos, permissões e segurança.
- 🤖 **[Script Instalador Oficial (`install.sh`)](install.sh)**: Automação completa para instalação, compilação, configuração do systemd, firewall e ajustes de kernel no Linux.

---

## 📄 Licença

Software proprietário. Todos os direitos reservados. Proibida redistribuição ou uso não autorizado.
