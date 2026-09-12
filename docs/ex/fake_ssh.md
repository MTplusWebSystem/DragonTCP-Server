# Exemplo: Gestão Completa do Fake SSH Integrado

O **Fake SSH** é um servidor SSH embutido no DragonTCP Server que roda em loopback (`127.0.0.1:2222`) com um banco de usuários isolado em formato JSON (`dragontcp-users.json`). Ele **não** cria usuários no sistema operacional Linux e **não** concede acesso a shell real (Bash/sh), funcionando exclusivamente como túnel de encaminhamento TCP (`direct-tcpip`).

---

## 1. Estrutura do Arquivo de Usuários (`dragontcp-users.json`)

O arquivo JSON é mantido automaticamente pelo DragonTCP, com hashes de senha gerados via `bcrypt`:

```json
{
  "version": 1,
  "users": [
    {
      "username": "cliente_vip",
      "password_hash": "$2a$10$7Z8L3Q1oF8j5U2wX4k7u9eK4H7m3L5q6W8r9T0y1U2i3O4p5A6s7D",
      "expires_at": "2026-10-12T15:30:00Z",
      "max_connections": 2,
      "disabled": false
    },
    {
      "username": "admin_teste",
      "password_hash": "$2a$10$9p0o8i7u6y5t4r3e2w1q0eK4H7m3L5q6W8r9T0y1U2i3O4p5A6s7D",
      "expires_at": "0001-01-01T00:00:00Z",
      "max_connections": 0,
      "disabled": false
    }
  ]
}
```

* `expires_at`: Data e hora de término do acesso. `"0001-01-01T00:00:00Z"` indica que a conta nunca expira.
* `max_connections`: Número de sessões SSH simultâneas autorizadas. `0` significa conexões ilimitadas.
* `disabled`: Permite suspender temporariamente o acesso do usuário sem remover seus dados.

---

## 2. Comandos de Linha de Comando (CLI)

> [!TIP]
> Ao utilizar `--config /etc/dragontcp/dragontcp.yaml`, o executável descobre automaticamente a localização da base de usuários configurada na chave `ssh.users`.

### Criar ou Atualizar Usuário

```bash
# Usuário com validade de 30 dias e máximo de 2 conexões simultâneas
dragontcp-server --config /etc/dragontcp/dragontcp.yaml \
  --ssh-user-add "joao_silva" \
  --ssh-user-password "SenhaForte@2026" \
  --ssh-user-days 30 \
  --ssh-user-max-connections 2
```

### Criar Usuário com Senha via Variável de Ambiente (Mais Seguro)

Evita que a senha fique registrada no histórico do bash (`~/.bash_history`) ou seja visível via `ps aux`:

```bash
export PASS_TEMPORARIA="MinhaSenhaUltraSegura!"
dragontcp-server --config /etc/dragontcp/dragontcp.yaml \
  --ssh-user-add "maria_souza" \
  --ssh-user-password-env PASS_TEMPORARIA \
  --ssh-user-days 60 \
  --ssh-user-max-connections 1
unset PASS_TEMPORARIA
```

### Listar Usuários Cadastrados

```bash
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-list
```

**Saída formatada no terminal:**
```text
USERNAME               EXPIRES                    MAX CONNECTIONS  STATUS
--------               -------                    ---------------  ------
joao_silva             2026-10-12 15:30 -03       2                active
maria_souza            2026-11-11 15:35 -03       1                active
teste_expirado         2026-08-01 10:00 -03       1                expired
```

### Excluir Usuário

```bash
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-user-delete "teste_expirado"
```

---

## 3. Menu Interativo de Gerenciamento (`--ssh-menu`)

Para administradores que preferem navegar visualmente pelo terminal:

```bash
dragontcp-server --config /etc/dragontcp/dragontcp.yaml --ssh-menu
```

O assistente exibirá as opções:
```text
========================================
   DragonTCP Fake SSH User Manager
========================================
1. List users
2. Add or update user
3. Delete user
4. Change password
5. Exit
Enter choice [1-5]: 
```

---

## 4. Script de Automação em Lote (Integração com Painéis / Bots)

Crie o arquivo `/usr/local/bin/criar-usuario-ssh.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

CONFIG="/etc/dragontcp/dragontcp.yaml"
USERNAME="${1:-}"
PASSWORD="${2:-}"
DAYS="${3:-30}"
MAX_CONNS="${4:-1}"

if [[ -z "$USERNAME" || -z "$PASSWORD" ]]; then
  echo "Uso: $0 <usuario> <senha> [dias] [max_conexoes]"
  exit 1
fi

export DRAGON_NEW_PASS="$PASSWORD"
dragontcp-server --config "$CONFIG" \
  --ssh-user-add "$USERNAME" \
  --ssh-user-password-env DRAGON_NEW_PASS \
  --ssh-user-days "$DAYS" \
  --ssh-user-max-connections "$MAX_CONNS"
unset DRAGON_NEW_PASS

echo "Sucesso: Usuário $USERNAME cadastrado por $DAYS dias com limite de $MAX_CONNS conexões."
```

Torne o script executável:
```bash
chmod +x /usr/local/bin/criar-usuario-ssh.sh
```

Exemplo de uso:
```bash
criar-usuario-ssh.sh "pedro" "Pedr0#2026" 30 2
```

---

## 5. Como o Cliente se Conecta ao Fake SSH

O aplicativo cliente (Android, Windows ou Linux) conecta-se à porta pública do DragonTCP (ex.: `53` ou `80`) e solicita a abertura do túnel para o host interno especial:

* **Host Destino:** `dragontcp-ssh.internal`
* **Porta Destino:** `2222`
* **Usuário SSH:** `joao_silva`
* **Senha SSH:** `SenhaForte@2026`

O servidor DragonTCP intercepta o tráfego destinado a `dragontcp-ssh.internal` e faz a ponte direta com o processo interno do Fake SSH, garantindo isolamento total do OpenSSH da máquina hospedeira.
