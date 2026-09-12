# Exemplo: UDPGW de Baixa Latência para Jogos e VoIP (Modo ABI Linux)

O **BadVPN UDPGW Integrado** do DragonTCP resolve um dos maiores problemas de túneis TCP: o encaminhamento eficiente de datagramas UDP para jogos competitivos (Free Fire, PUBG, COD Mobile), chamadas de áudio e vídeo (WhatsApp, Telegram, Discord) e consultas DNS rápidas.

No DragonTCP, o modo **`native`** implementa chamadas diretas de ABI do Linux que descarregam os datagramas UDP diretamente na interface física de rede (NIC), superando amplamente implementações convencionais de BadVPN em espaço de usuário.

---

## 1. Por que o Modo ABI Nativo é Superior?

| Recurso | UDPGW Convencional (BadVPN) | DragonTCP UDPGW Modo `native` | Impacto Prático |
| :--- | :--- | :--- | :--- |
| **Encaminhamento de Rota** | Recalcula rota via pilha IP do kernel a cada pacote | `SO_BINDTODEVICE` na NIC física (`eth0`/`ens3`) | Ignora tabela de rotas, reduzindo latência |
| **Interrupções do Kernel** | Acorda o processo por interrupção de software (softirq) | `SO_BUSY_POLL` (polling ativo de 50 µs na fila da NIC) | **Latência quase zero** e eliminação de jitter |
| **Tamanho dos Buffers** | Buffers padrão pequenos do sistema (128–256 KB) | Buffers forçados de 4 MB (`SO_RCVBUFFORCE`) | Zero perda de pacotes em picos de rajada |
| **QoS / Priorização** | Cabeçalho IP normal | `IPTOS_LOWDELAY` (TOS `0x10`) ativado | Prioridade em switches e roteadores |
| **Fragmentação MTU** | Sujeito a "buracos negros" de PMTU | `IP_PMTUDISC_DONT` configurado | Não descarta pacotes por falha de MTU |

---

## 2. Configuração no `dragontcp.yaml`

```yaml
udpgw:
  enable: true                     # Ativa o relay UDPGW integrado
  listen: "127.0.0.1:7400"         # Escuta local em loopback
  internal_host: "dragontcp-udpgw.internal" # Host interno interceptado pelo Fake SSH
  max_clients: 10000               # Suporte a até 10.000 clientes UDP concorrentes
  mode: "native"                   # Ativa a descarga ABI Linux de alta performance
  interface: "auto"                # "auto" detecta a placa padrão (ex: eth0, ens3)
  busy_poll_us: 50                 # 50 microssegundos de busy-polling na fila da NIC
  debug: false
```

### Como Verificar a Interface de Rede Física

Se desejar especificar manualmente a placa de rede em vez de usar `"auto"`, descubra a placa de saída com o comando:

```bash
ip route get 8.8.8.8 | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}'
```

Exemplo de resultado:
```text
eth0
```

Se for `eth0`, ajuste no YAML:
```yaml
udpgw:
  mode: "native"
  interface: "eth0"
```

---

## 3. Requisitos e Privilégios do Sistema

Para que o DragonTCP possa aplicar `SO_BINDTODEVICE` e `SO_RCVBUFFORCE`, o processo precisa de permissões de rede elevadas:

### Opção A: Executar como `root` (Padrão no Systemd)
A unidade `/etc/systemd/system/dragontcp.service` já executa como `User=root`.

### Opção B: Executar como Usuário Sem Privilégios com `setcap`
Se preferir executar como usuário comum (ex.: `dragontcp`):

```bash
setcap 'cap_net_bind_service,cap_net_raw,cap_net_admin=+ep' /usr/local/bin/dragontcp-server
```

---

## 4. Como os Aplicativos Clientes se Conectam

Clientes VPN (Android, iOS ou Desktop) utilizam a biblioteca `badvpn-udpgw` ou `tun2socks` apontando para o host interno interceptado:

* **Endereço UDPGW:** `127.0.0.1:7400` (ou `dragontcp-udpgw.internal:7400`)
* **Porta local:** `7400`
* **Buffer UDP padrão:** `65535`

### Exemplo em clientes Android (HTTP Injector, HTTP Custom, LiteVPN):
No campo de configuração do BadVPN / UDPGW do aplicativo:
```text
127.0.0.1:7400
```
ou
```text
dragontcp-udpgw.internal:7400
```

O tráfego de voz e pacotes de jogos são instantaneamente encapsulados no túnel e descarregados pelo DragonTCP diretamente na interface física do servidor com latência estável e sem oscilações de ping.
