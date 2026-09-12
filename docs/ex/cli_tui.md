# Exemplo: Gerenciamento com a CLI TUI Interativa (`dragontcp-cli`)

O DragonTCP inclui uma interface de terminal rica (TUI) desenvolvida com a suíte moderna **Bubble Tea**, **Lipgloss** e **Huh**, permitindo monitorar o servidor em tempo real, auditar tráfego, criar e suspender usuários e desconectar sessões suspeitas com experiência visual de alto nível.

---

## 1. Como Iniciar a TUI

### Inicialização Padrão
Se o executável estiver instalado em `/usr/local/bin`:

```bash
dragontcp-cli
```

### Inicialização com Parâmetros Customizados
```bash
dragontcp-cli \
  --admin-addr "http://127.0.0.1:53080" \
  --config "/etc/dragontcp/dragontcp.yaml" \
  --users "/etc/dragontcp/dragontcp-users.json"
```

---

## 2. Visão Geral das Telas e Recursos

### 🖥️ 1. Status do Servidor e Telemetria
* Exibe o estado operacional do daemon: `Online`, PID, uptime e horário de início.
* Contadores de tráfego em tempo real:
  * Vazão acumulada de Upload e Download (formatados em KiB, MiB ou GiB).
  * Conexões físicas ativas (`active_tunnels`) e sessões lógicas abertas (`active_sessions`).
  * Total de sessões abertas e fechadas no ciclo de vida.
  * Registros de `Push`, `Pull`, `Data` e `Wait`.
  * Taxa de erros de autenticação ou transporte.

### 📊 2. Métricas do Sistema
* Consumo de CPU e total de núcleos.
* Número de goroutines ativas simultâneas.
* Memória RAM alocada pelo runtime Go (`AllocBytes`, `TotalAlloc`, `SysBytes`).
* Contador de ciclos do Garbage Collector (`NumGC`).

### 👥 3. Gestão de Usuários Fake SSH
* Lista interativa de contas de túnel com barra de busca rápida (`/`).
* Cartão de detalhes do usuário com data de expiração, status (`active`, `expired`, `disabled`) e limite de conexões.
* **Criação Interativa de Usuário**: Formulário moderno (via biblioteca *Huh*) solicitando nome, senha, dias de validade e limite de conexões.
* **Ações Rápidas**: Bloquear/Desbloquear usuário e exclusão imediata com confirmação.

### 🌐 4. Monitor de Conexões Ativas e "Kill Session"
* Lista de todas as sessões lógicas em andamento no servidor, detalhando:
  * `SessionID` (16 bytes hexadecimais).
  * Host e porta de destino (ex.: `dragontcp-ssh.internal:2222` ou `1.1.1.1:443`).
  * Volume de dados atualmente no buffer (`buffered_bytes`).
  * Tempo desde o último pacote trafegado (`last_seen`).
* **Kill Session (Derrubada Forçada)**: Selecione qualquer conexão e pressione a opção de encerramento para cortar o túnel e fechar o socket remoto instantaneamente via API REST (`/api/connections/kill`).

### 📜 5. Visualizador de Logs
* Acompanhamento das últimas linhas do log interno do servidor, com suporte a rolagem e paginação sem sair da interface.

### ⚙️ 6. Editor Visual de Configurações YAML
* Navegue pelas seções de Rede, Segurança, Performance, Fake SSH e UDPGW.
* Altere portas, limites e timeouts diretamente pela TUI com validação e salvamento no arquivo `/etc/dragontcp/dragontcp.yaml`.

---

## 3. Atalhos de Teclado Principais

| Tecla | Ação |
| :---: | :--- |
| `↑` / `↓` ou `k` / `j` | Mover cursor na lista ou menu |
| `Enter` | Selecionar item, abrir submenu ou confirmar formulário |
| `Esc` | Voltar à tela anterior |
| `/` | Ativar modo de busca / filtro na lista de usuários ou conexões |
| `q` / `Ctrl+C` | Sair da aplicação |
| `Tab` / `Shift+Tab` | Alternar campos em formulários |
