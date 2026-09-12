# DragonTCP — Exemplos Práticos de Configuração (`docs/ex`)

Esta pasta contém cenários de implantação do mundo real, arquivos de configuração comentados e receitas práticas para operar o **DragonTCP Server** e a **CLI Administrativa** com máxima eficiência e segurança.

---

## Índice de Exemplos

| Exemplo | Documento | Descrição Principal |
| :--- | :--- | :--- |
| **1. Servidor de Produção** | [producao.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/ex/producao.md) | Configuração completa para servidores VPS Linux de alto tráfego com portas 53 e 80 simultâneas, buffers TCP e serviço systemd. |
| **2. Gerenciamento Fake SSH** | [fake_ssh.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/ex/fake_ssh.md) | Criação de contas, limites de concorrência por usuário, expiração programada, menu interativo e automação via terminal. |
| **3. UDPGW para Jogos e VoIP** | [udpgw_gaming.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/ex/udpgw_gaming.md) | Configuração do modo ABI Nativo Linux com `SO_BUSY_POLL` e descarga direta na placa de rede para ultra-baixa latência. |
| **4. Payloads HTTP e Bypass DPI** | [http_payloads.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/ex/http_payloads.md) | Como configurar e utilizar injeção de cabeçalhos HTTP (WebSocket 101 Switching Protocols) e payloads zero-rated de operadoras. |
| **5. Operação via CLI TUI** | [cli_tui.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/ex/cli_tui.md) | Painel visual interativo no terminal: monitoramento de tráfego, telemetria de CPU/RAM, desconexão de clientes e edição de configurações. |

---

## Como Utilizar Estes Exemplos

1. **Escolha o cenário** adequado para o seu caso de uso na tabela acima.
2. Copie os modelos de configuração YAML ou scripts fornecidos em cada documento.
3. Adapte os valores específicos do seu ambiente (como interfaces de rede, IPs, portas e senhas).
4. Consulte [parameters.md](file:///c:/Users/MT-PLUS__pk-minato/OneDrive/Área%20de%20Trabalho/PROJETOS/dragontcp/core/cmd/dragontcp-server/docs/parameters.md) para detalhes exaustivos sobre qualquer diretiva citada nos exemplos.
