# DragonTCP — Guia Completo de Parâmetros e Configuração

Este documento é a referência definitiva de todos os parâmetros de configuração do **DragonTCP Server** e do **DragonTCP CLI**, abrangendo flags de linha de comando, diretivas do arquivo `dragontcp.yaml` (formatos aninhado e plano, compatíveis com *snake_case* e *kebab-case*) e as opções da interface administrativa TUI.

---

## Sumário

1. [Ordem de Precedência](#-ordem-de-precedência)
2. [Parâmetros de Rede e Escuta](#-parâmetros-de-rede-e-escuta)
3. [Segurança e Controle de Acesso](#-segurança-e-controle-de-acesso)
4. [Resolução DNS Interna](#-resolução-dns-interna)
5. [Tuning de Performance e Buffers](#-tuning-de-performance-e-buffers)
6. [Diagnóstico, Logs e Telemetria](#-diagnóstico-logs-e-telemetria)
7. [Fake SSH Integrado](#-fake-ssh-integrado)
8. [BadVPN UDPGW Integrado (Relay UDP)](#-badvpn-udpgw-integrado-relay-udp)
9. [API de Administração e CLI TUI](#-api-de-administração-e-cli-tui)
10. [Tabela Resumo de Equivalência (CLI vs. YAML)](#-tabela-resumo-de-equivalência-cli-vs-yaml)

---

## 🎯 Ordem de Precedência

O DragonTCP adota uma hierarquia estrita de três camadas para resolução de configurações:

```text
┌─────────────────────────────────────────────────────────┐
│ 1. Flags CLI (--port, --debug, --udpgw-mode, ...)       │  <- Maior prioridade (sobrescreve tudo)
├─────────────────────────────────────────────────────────┤
│ 2. Arquivo YAML (--config /etc/dragontcp/dragontcp.yaml) │  <- Prioridade intermediária
├─────────────────────────────────────────────────────────┤
│ 3. Valores Padrão Embutidos (Hardcoded Defaults)        │  <- Menor prioridade (usados se omitidos)
└─────────────────────────────────────────────────────────┘
```

> [!NOTE]
> Se um parâmetro for especificado na linha de comando via flag (ex.: `--port 443`), ele terá precedência absoluta sobre o valor contido no arquivo YAML (ex.: `port: 53`).

---

## 🌐 Parâmetros de Rede e Escuta

Configuram os pontos de entrada do tráfego TCP bruto que chega ao servidor.

### `--host`
* **Equivalente YAML:** `host`
* **Tipo:** `string` (IPv4 ou IPv6)
* **Padrão:** `"0.0.0.0"`
* **Descrição:** Endereço IP da interface de rede onde o servidor aguardará conexões.
  * `"0.0.0.0"`: Escuta em todas as interfaces IPv4 ativas.
  * `"::"`: Escuta em todas as interfaces IPv6 e IPv4 (dual-stack).
  * `"127.0.0.1"`: Restringe conexões apenas ao loopback local (ideal se houver proxy reverso ou CDN local).

### `--port`
* **Equivalente YAML:** `port`
* **Tipo:** `int` (1 a 65535)
* **Padrão:** `53`
* **Descrição:** Porta TCP principal de escuta. A porta `53` (padrão DNS) é altamente recomendada em cenários de bypass de DPI e firewalls restritos, pois a maioria das operadoras e redes corporativas não bloqueia tráfego na porta 53.

### `--port-alt`
* **Equivalente YAML:** `port_alt` ou `port-alt`
* **Tipo:** `int` (0 a 65535)
* **Padrão:** `80`
* **Descrição:** Segunda porta TCP simultânea atendida pela mesma instância do DragonTCP Server.
  * Define `0` para desativar a porta secundária.
  * Padrão `80` permite atender clientes via HTTP convencional ou payloads com *host header injection* enquanto a porta 53 atende DNS/Direct.

---

## 🔒 Segurança e Controle de Acesso

### `--token`
* **Equivalente YAML:** `token`
* **Tipo:** `string`
* **Padrão:** `""` (vazio = autenticação de token desabilitada)
* **Descrição:** Chave secreta compartilhada obrigatória para abertura de novos túneis (`ModeOpen`).
  * Quando configurado, qualquer solicitação `ModeOpen` com token inválido ou ausente é imediatamente rejeitada com status `StatusError` ("authentication failed").
  * A comparação é realizada em tempo constante (`subtle.ConstantTimeCompare`) para impedir ataques de temporização (*timing attacks*).

### `--max-connections`
* **Equivalente YAML:** `max_connections` ou `max-connections`
* **Tipo:** `int`
* **Padrão:** `20000`
* **Descrição:** Número máximo de conexões físicas de clientes atendidas simultaneamente.
  * Protege o servidor contra exaustão de *file descriptors* (FDs) e ataques de negação de serviço (DoS).
  * Conexões que excederem esse limite são rejeitadas imediatamente no *accept loop*.

### `--allow-private`
* **Equivalente YAML:** `allow_private` ou `allow-private`
* **Tipo:** `bool`
* **Padrão:** `false`
* **Descrição:** Determina se os clientes podem abrir túneis para endereços IP privados ou de loopback (RFC 1918, RFC 3927, RFC 5737, etc.).
  * `false` (Recomendado): Bloqueia destinos como `127.0.0.1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` e redes de documentação.
  * `true`: Permite que clientes façam túnel para serviços locais da máquina hospedeira.

> [!CAUTION]
> Manter `--allow-private=false` em servidores públicos é essencial para evitar ataques de SSRF (Server-Side Request Forgery) contra a infraestrutura interna da sua nuvem (ex.: metadados de instâncias AWS/GCP em `169.254.169.254`). Serviços internos registrados explicitamente pelo DragonTCP (como `dragontcp-ssh.internal`) continuam acessíveis mesmo com esta flag desativada.

---

## 🧠 Resolução DNS Interna

O DragonTCP possui um resolver DNS embutido com cache em memória de altíssima velocidade para evitar latência em cada abertura de túnel.

### `--dns-cache-ttl`
* **Equivalente YAML:** `dns_cache_ttl` ou `dns-cache-ttl`
* **Tipo:** `duration` (ex.: `30s`, `1m`, `5m`)
* **Padrão:** `30s`
* **Descrição:** Tempo de vida dos registros de resolução DNS no cache interno antes de consultar o resolver do sistema operacional.

### `--dns-cache-size`
* **Equivalente YAML:** `dns_cache_size` ou `dns-cache-size`
* **Tipo:** `int`
* **Padrão:** `4096`
* **Descrição:** Quantidade máxima de hostnames únicos armazenados no cache. Quando o limite é atingido, o cache sofre um reset controlado para preservar memória.

---

## ⚡ Tuning de Performance e Buffers

### `--tcp-buffer`
* **Equivalente YAML:** `tcp_buffer` ou `tcp-buffer`
* **Tipo:** `int` (bytes)
* **Padrão:** `0` (usa o *autotuning* do kernel do sistema operacional)
* **Descrição:** Tamanho fixo em bytes dos buffers de leitura (`SO_RCVBUF`) e escrita (`SO_SNDBUF`) das conexões TCP estabelecidas.
  * O valor padrão `0` permite que o algoritmo TCP do Linux ajuste dinamicamente a janela de recepção (BDP). Em links de 1 Gbps+ com alta latência, valores manuais como `1048576` (1 MiB) podem ser avaliados.

### `--chunk-max`
* **Equivalente YAML:** `chunk_max` ou `chunk-max`
* **Tipo:** `int` (bytes, mín. `32`, máx. `1048576` / 1 MiB)
* **Padrão:** `1048576` (1 MiB)
* **Descrição:** Tamanho máximo de payload por frame individual transmitido no túnel.
  * Permite ao cliente adaptar dinamicamente o tamanho do frame conforme a estabilidade e latência da rede.

### `--chunk-buffered`
* **Equivalente YAML:** `chunk_buffered` ou `chunk-buffered`
* **Tipo:** `int` (unidades de 64 KiB, mín. `8`)
* **Padrão:** `32` (~2 MiB por sessão)
* **Descrição:** Capacidade do buffer de retenção de dados de download por sessão ativa.
  * Cada unidade representa 64 KiB de dados pré-carregados da conexão remota.
  * `32` * 64 KiB = 2.097.152 bytes (~2 MiB). Evita engasgos no streaming e permite rajadas de download sem perda de pacotes.

### `--chunk-poll-wait`
* **Equivalente YAML:** `chunk_poll_wait` ou `chunk-poll-wait`
* **Tipo:** `duration` (ex.: `50ms`, `200ms`, `500ms`)
* **Padrão:** `200ms`
* **Descrição:** Intervalo máximo que o servidor aguarda no *long-poll* quando o cliente solicita dados de download e o buffer interno está temporariamente vazio.
  * Se dados chegarem antes desse intervalo, o servidor responde imediatamente.

### `--chunk-session-timeout`
* **Equivalente YAML:** `chunk_session_timeout` ou `chunk-session-timeout`
* **Tipo:** `duration` (ex.: `1m`, `2m`, `5m`)
* **Padrão:** `2m` (2 minutos)
* **Descrição:** Tempo de inatividade após o qual uma sessão lógica sem tráfego de upload/download é considerada órfã e seus recursos (sockets remotos e buffers) são liberados.

---

## 📊 Diagnóstico, Logs e Telemetria

### `--debug`
* **Equivalente YAML:** `debug`
* **Tipo:** `bool`
* **Padrão:** `false`
* **Descrição:** Ativa logs operacionais detalhados no `stdout`/`stderr`, incluindo conexões aceitas, classificação do sniffer (`WIRE peer=... mode=mux_v2`), erros de autenticação e desconexões.

### `--debug-chunks`
* **Equivalente YAML:** `debug_chunks` ou `debug-chunks`
* **Tipo:** `bool`
* **Padrão:** `false`
* **Descrição:** Ativa o log de cada registro binário trafegado (requisições e respostas de upload/download).
  * > [!WARNING]
  * > Extremamente verboso. Deve ser utilizado exclusivamente para depuração de novos clientes ou engenharia reversa de falhas de protocolo.

### `--debug-stats-interval`
* **Equivalente YAML:** `debug_stats_interval` ou `debug-stats-interval`
* **Tipo:** `duration`
* **Padrão:** `5s`
* **Descrição:** Intervalo entre impressões periódicas de estatísticas no console (túneis ativos, sessões ativas, vazão acumulada de upload/download e taxa de erros). Definir `0` para desativar.

---

## 🔑 Fake SSH Integrado

O DragonTCP possui um daemon SSH embutido com banco de usuários independente, exposto em loopback e interceptado internamente pelo protocolo.

### Configurações Gerais do Daemon Fake SSH

| Flag CLI | Chave YAML | Tipo | Padrão | Descrição |
| :--- | :--- | :---: | :---: | :--- |
| `--ssh-enable` | `ssh.enable` / `ssh_enable` | `bool` | `true` | Ativa ou desativa o serviço de Fake SSH |
| `--ssh-listen` | `ssh.listen` / `ssh_listen` | `string` | `"127.0.0.1:2222"` | Endereço local onde o daemon SSH escuta |
| `--ssh-internal-host` | `ssh.internal_host` / `ssh_internal_host` | `string` | `"dragontcp-ssh.internal"` | Nome de host reservado interceptado pelo servidor |
| `--ssh-host-key` | `ssh.host_key` / `ssh_host_key` | `string` | `"dragontcp_ssh_host_key"` | Caminho da chave privada ed25519 do servidor (gerada na 1ª execução) |
| `--ssh-users` | `ssh.users` / `ssh_users` | `string` | `"dragontcp-users.json"` | Caminho do arquivo JSON da base de usuários |

### Flags de Gerenciamento de Usuários via CLI

Quando qualquer uma das flags abaixo é invocada, o servidor executa a ação de gerenciamento solicitada e encerra imediatamente a execução (`os.Exit(0)`), permitindo automação via scripts e cronjobs:

#### `--ssh-user-add <username>`
Cria uma nova conta de usuário ou atualiza uma existente no arquivo de usuários.
* **Flags complementares:**
  * `--ssh-user-password <senha>`: Define a senha em texto claro (armazenada com hash `bcrypt`).
  * `--ssh-user-password-env <VAR>`: Lê a senha a partir da variável de ambiente indicada (mais seguro contra inspeção de `ps aux`).
  * `--ssh-user-days <dias>`: Validade da conta em dias a partir do momento da criação. Padrão `0` (sem expiração).
  * `--ssh-user-max-connections <n>`: Limite de conexões SSH simultâneas permitidas para o usuário. Padrão `1`. `0` significa ilimitado.

#### `--ssh-user-delete <username>`
Remove definitivamente o usuário especificado da base.

#### `--ssh-user-list`
Exibe no terminal a tabela formatada com todos os usuários cadastrados, data de expiração, conexões simultâneas máximas e status (`active`, `expired`, `disabled`).

#### `--ssh-menu`
Abre um assistente de terminal interativo com menu numérico para cadastro, edição de limites, alteração de senhas, listagem e exclusão de contas.

---

## 🎮 BadVPN UDPGW Integrado (Relay UDP)

O serviço UDPGW integrado permite aos clientes tunelados rotear tráfego UDP (jogos online, chamadas VoIP WhatsApp/Discord e transmissões de baixa latência) sem a necessidade de instalar o binário externo do BadVPN.

### Configurações Gerais do UDPGW

| Flag CLI | Chave YAML | Tipo | Padrão | Descrição |
| :--- | :--- | :---: | :---: | :--- |
| `--udpgw-enable` | `udpgw.enable` / `udpgw_enable` | `bool` | `true` | Ativa o relay UDPGW integrado |
| `--udpgw-listen` | `udpgw.listen` / `udpgw_listen` | `string` | `"127.0.0.1:7400"` | Endereço de escuta do relay UDPGW |
| `--udpgw-internal-host` | `udpgw.internal_host` / `udpgw_internal_host` | `string` | `"dragontcp-udpgw.internal"` | Host interceptado para encaminhamento via SSH direct-tcpip |
| `--udpgw-max-clients` | `udpgw.max_clients` / `udpgw_max_clients` | `int` | `10000` | Número máximo de clientes UDP concorrentes atendidos |
| `--udpgw-debug` | `udpgw.debug` / `udpgw_debug` | `bool` | `false` | Emite logs detalhados de erros e frames UDP |

### 🚀 Modos de Descarga UDP (`--udpgw-mode` / `udpgw.mode`)

O DragonTCP implementa três modos de descarga de tráfego UDP:

#### 1. `native` (Padrão — ABI Linux de Alta Performance)
* **Requisitos:** Linux com privilégios de `root` ou permissão `CAP_NET_RAW`.
* **Mecanismo:** Utiliza chamadas diretas de sistema Linux ABI:
  * `SO_BINDTODEVICE`: Associa os sockets UDP diretamente à interface física de rede (ex.: `eth0`), ignorando a reavaliação contínua da tabela de rotas do kernel.
  * `SO_BUSY_POLL`: Habilita *busy polling* ativo na fila da placa de rede, reduzindo a latência de interrupção a praticamente zero.
  * Buffers forçados de 4 MB (`SO_RCVBUFFORCE`, `SO_SNDBUFFORCE`).
  * `IPTOS_LOWDELAY` (TOS 0x10) para priorização de pacotes de voz e jogos.
  * `IP_PMTUDISC_DONT` para evitar descarte de pacotes por descoberta agressiva de MTU.

#### 2. `tun` (Dispositivo TUN do Kernel)
* **Requisitos:** `/dev/net/tun` disponível no sistema.
* **Mecanismo:** Injeta pacotes IPv4/UDP diretamente no dispositivo TUN do kernel, ideal para integração com pilhas de roteamento customizadas e WireGuard.

#### 3. `standard` (Compatibilidade Universal)
* **Requisitos:** Qualquer sistema operacional (Linux, Windows, macOS, BSD).
* **Mecanismo:** Loop tradicional user-space com `ReadFromUDP`/`WriteToUDP`. Utilizado como fallback quando não há privilégios de root.

### Configurações Adicionais do Modo Nativo

* **`--udpgw-interface` (`udpgw.interface`):**
  * Nome da placa de rede física utilizada para o `SO_BINDTODEVICE` (ex.: `"eth0"`, `"ens3"`, `"enp0s3"`).
  * Valor `"auto"` (padrão): Detecta automaticamente a placa de saída padrão do sistema.
* **`--udpgw-busy-poll` (`udpgw.busy_poll` ou `udpgw.busy_poll_us`):**
  * Tempo de *busy polling* na fila da NIC em microssegundos (padrão: `50`).
  * Definir `0` para desativar caso o driver de rede não suporte.

---

## 🖥️ API de Administração e CLI TUI

### `--admin-addr`
* **Equivalente YAML:** `admin_addr` ou `admin-addr`
* **Tipo:** `string` (host:port)
* **Padrão:** `"127.0.0.1:53080"`
* **Descrição:** Endereço da API REST local HTTP do DragonTCP. Fornece telemetria em tempo real para a interface de terminal TUI (`dragontcp-cli`).
  * Se configurado como `""` (string vazia), a API administrativa é totalmente desativada.

### Endpoints REST Disponíveis

* `GET /api/status`: Retorna status do processo, uptime, portas, conexões ativas, sessões abertas/fechadas, bytes up/down e flags ativas.
* `GET /api/metrics`: Retorna métricas do runtime Go (CPU, goroutines, memória alocada, coletas de GC).
* `GET /api/connections`: Lista todas as conexões ativas com detalhes de `session_id`, destino, bytes no buffer e tempo de inatividade.
* `POST /api/connections/kill`: Encerra forçadamente uma sessão ativa enviando o payload `{"session_id": "hex"}`.
* `GET /api/logs`: Retorna as últimas linhas do log operacional do servidor.
* `GET /api/users`: Lista os usuários da base Fake SSH.
* `POST /api/users/block`: Alterna o bloqueio de um usuário (`{"username": "nome", "disabled": true}`).
* `POST /api/restart`: Reinicia o processo graciosamente.

### Parâmetros da CLI TUI (`dragontcp-cli`)

A ferramenta de terminal pode ser executada com os seguintes argumentos:

```bash
dragontcp-cli [flags]
```

* `--admin-addr <url>`: URL da API administrativa do servidor (padrão: `http://127.0.0.1:53080`).
* `--config <path>`: Caminho do arquivo YAML do DragonTCP (padrão: `dragontcp.yaml`).
* `--users <path>`: Caminho da base de usuários JSON do Fake SSH (padrão: `dragontcp-users.json`).

---

## 📋 Tabela Resumo de Equivalência (CLI vs. YAML)

| Flag CLI | Chave YAML (Aninhado) | Chave YAML (Plano) | Padrão | Tipo |
| :--- | :--- | :--- | :---: | :---: |
| `--config` | *N/A* | *N/A* | `""` | `string` |
| `--host` | `host` | `host` | `"0.0.0.0"` | `string` |
| `--port` | `port` | `port` | `53` | `int` |
| `--port-alt` | `port_alt` / `port-alt` | `port_alt` | `80` | `int` |
| `--token` | `token` | `token` | `""` | `string` |
| `--max-connections` | `max_connections` / `max-connections` | `max_connections` | `20000` | `int` |
| `--allow-private` | `allow_private` / `allow-private` | `allow_private` | `false` | `bool` |
| `--dns-cache-ttl` | `dns_cache_ttl` / `dns-cache-ttl` | `dns_cache_ttl` | `30s` | `duration` |
| `--dns-cache-size` | `dns_cache_size` / `dns-cache-size` | `dns_cache_size` | `4096` | `int` |
| `--tcp-buffer` | `tcp_buffer` / `tcp-buffer` | `tcp_buffer` | `0` | `int` |
| `--chunk-max` | `chunk_max` / `chunk-max` | `chunk_max` | `1048576` | `int` |
| `--chunk-buffered` | `chunk_buffered` / `chunk-buffered` | `chunk_buffered` | `32` | `int` |
| `--chunk-poll-wait` | `chunk_poll_wait` / `chunk-poll-wait` | `chunk_poll_wait` | `200ms` | `duration` |
| `--chunk-session-timeout` | `chunk_session_timeout` / `chunk-session-timeout` | `chunk_session_timeout` | `2m` | `duration` |
| `--debug` | `debug` | `debug` | `false` | `bool` |
| `--debug-chunks` | `debug_chunks` / `debug-chunks` | `debug_chunks` | `false` | `bool` |
| `--debug-stats-interval` | `debug_stats_interval` / `debug-stats-interval` | `debug_stats_interval` | `5s` | `duration` |
| `--admin-addr` | `admin_addr` / `admin-addr` | `admin_addr` | `"127.0.0.1:53080"` | `string` |
| `--ssh-enable` | `ssh.enable` | `ssh_enable` / `ssh-enable` | `true` | `bool` |
| `--ssh-listen` | `ssh.listen` | `ssh_listen` / `ssh-listen` | `"127.0.0.1:2222"` | `string` |
| `--ssh-internal-host` | `ssh.internal_host` / `ssh.internal-host` | `ssh_internal_host` | `"dragontcp-ssh.internal"` | `string` |
| `--ssh-host-key` | `ssh.host_key` / `ssh.host-key` | `ssh_host_key` | `"dragontcp_ssh_host_key"` | `string` |
| `--ssh-users` | `ssh.users` | `ssh_users` / `ssh-users` | `"dragontcp-users.json"` | `string` |
| `--udpgw-enable` | `udpgw.enable` | `udpgw_enable` / `udpgw-enable` | `true` | `bool` |
| `--udpgw-listen` | `udpgw.listen` | `udpgw_listen` / `udpgw-listen` | `"127.0.0.1:7400"` | `string` |
| `--udpgw-internal-host` | `udpgw.internal_host` / `udpgw.internal-host` | `udpgw_internal_host` | `"dragontcp-udpgw.internal"` | `string` |
| `--udpgw-max-clients` | `udpgw.max_clients` / `udpgw.max-clients` | `udpgw_max_clients` | `10000` | `int` |
| `--udpgw-mode` | `udpgw.mode` | `udpgw_mode` / `udpgw-mode` | `"native"` | `string` |
| `--udpgw-interface` | `udpgw.interface` | `udpgw_interface` / `udpgw-interface` | `"auto"` | `string` |
| `--udpgw-busy-poll` | `udpgw.busy_poll` / `udpgw.busy_poll_us` | `udpgw_busy_poll` | `50` | `int` |
| `--udpgw-debug` | `udpgw.debug` | `udpgw_debug` / `udpgw-debug` | `false` | `bool` |
