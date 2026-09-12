# Exemplo: Payloads HTTP, WebSocket e Bypass de DPI

O **DragonTCP Server** conta com um motor de inspeção não destrutiva (`sniffWire` e `bhttp.go`) que detecta automaticamente requisições HTTP enviadas por clientes em redes móveis restritas (3G, 4G, 5G), respondendo com `HTTP/1.1 101 Switching Protocols` e convertendo imediatamente a conexão em um túnel binário multiplexado de alta velocidade.

---

## 1. Como Funciona o Handshake de Payload

Muitas operadoras de telefonia e provedores de internet aplicam inspeção profunda de pacotes (DPI) e filtros por SNI/Host, permitindo tráfego gratuito (*zero-rated*) ou irrestrito apenas para determinados domínios (ex.: portais de recarga, redes sociais ou bancos).

O DragonTCP permite que o cliente envie uma requisição HTTP tradicional simulando acesso a esses hosts. Ao identificar o cabeçalho `Upgrade: websocket` ou método HTTP válido, o servidor responde com o código de status `101`:

```text
[Cliente Móvel]                                               [DragonTCP Server (Porta 80 ou 53)]
      |                                                                       |
      |=== (1) GET / HTTP/1.1                                                |
      |        Host: portalrecarga.vivo.com.br                                |
      |        Upgrade: websocket                                             |
      |        Connection: Upgrade\r\n\r\n ==================================>|
      |                                                                       |
      |<== (2) HTTP/1.1 101 Switching Protocols                               |
      |        Upgrade: websocket                                             |
      |        Connection: Upgrade\r\n\r\n ===================================|
      |                                                                       |
      |=== (3) [Fluxo Binário DragonTCP Mux v2 (33/9 bytes)] ================>| (Túnel Ativo)
```

---

## 2. Exemplos Práticos de Payloads de Clientes

Os aplicativos clientes (como HTTP Custom, HTTP Injector ou clientes Dragon customizados) podem utilizar formatos padrão de injeção:

### Exemplo A: Payload Básico WebSocket (Recomendado)
```http
GET / HTTP/1.1[crlf]Host: portalrecarga.vivo.com.br[crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf][crlf]
```

### Exemplo B: Payload com Cabeçalhos de Proxy Reverso e CDN
```http
GET / HTTP/1.1[crlf]Host: [app_host][crlf]X-Online-Host: [app_host][crlf]X-Forward-Host: [app_host][crlf]Upgrade: websocket[crlf]Connection: Upgrade[crlf]User-Agent: Mozilla/5.0 (Android; Mobile)[crlf][crlf]
```

---

## 3. Compatibilidade Híbrida: HTTP + Mux v2

O teste automatizado `TestHTTPPayloadHandshakeAndMuxV2` do DragonTCP garante que, após o encerramento dos cabeçalhos HTTP (`\r\n\r\n`), a conexão física é imediatamente transferida para o `muxServerConn`:

1. O cliente envia o handshake HTTP com o `Host` desejado.
2. O DragonTCP envia a confirmação `101 Switching Protocols`.
3. O cliente inicia imediatamente o envio de frames v2 (`ModeOpen`, `ModeUpload`, `ModeDownload`, etc.) com cabeçalhos binários de 33 bytes.
4. Toda a multiplexação concorrente passa a operar com proteção contra firewalls de DPI.

---

## 4. Configuração Recomendada no `dragontcp.yaml`

Para suportar tráfego HTTP sem interferir no tráfego DNS direto:

```yaml
host: "0.0.0.0"
port: 53        # Clientes diretos ou UDP/DNS
port_alt: 80    # Clientes com payload HTTP (porta 80 padrão de navegação)
```

Dessa forma, os clientes que necessitam de injeção de payload conectam-se na porta **80**, enquanto clientes diretos conectam-se na porta **53**.
