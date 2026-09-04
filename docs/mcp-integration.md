# MCP integration and conformance

Cosmo is an MCP client and gateway. An MCP server owns its tool names,
descriptions, input/output JSON Schemas and business authorization. Cosmo owns
workspace installation, agent selection, credential storage, outbound-network
policy and invocation limits. Neither side may depend on the other's database,
UI, agent model or domain types.

```text
Model / agent
    |
    v
Cosmo tool registry -- complete MCP contracts --> Cosmo MCP client
                                                    |
                                      Streamable HTTP + selected auth profile
                                                    |
                                                    v
                                  Any standards-compliant MCP server
                                                    |
                                      its own adapters and business policies
```

## Server contract

A server connected to Cosmo must:

1. expose an absolute HTTP(S) Streamable HTTP endpoint;
2. support MCP initialization and `tools/list` / `tools/call`;
3. publish a valid `inputSchema` for every tool and use MCP-compatible tool
   names (letters, numbers, `_`, `-` and `.`, up to 128 bytes);
4. return business failures as a tool result with `isError: true`, reserving
   JSON-RPC errors for protocol failures;
5. validate its own access token and business permissions. A successful MCP
   handshake is not permission to access its downstream system.

Cosmo stores the complete tool object returned by `tools/list`, including
nested schemas, `outputSchema`, annotations and metadata. The smaller parameter
list in the editor is only a UI projection and is not used to reconstruct MCP
calls.

## Add any MCP server to Cosmo

1. Allow the destination hostname in `TOOL_EGRESS_ALLOWED_HOSTS` only when the
   server is on a private network. Public destinations need no exception.
2. In **Workspace → Tool**, create a tool with kind **MCP** and enter the full
   endpoint, including `/mcp` when required by the server.
3. Select one independent authentication profile:
   `none`, bearer, custom header, OAuth 2.0 client credentials, or Microsoft
   Entra on-behalf-of.
4. Save, then select **Discover MCP tools**. Review the discovered descriptions
   and schemas before publishing.
5. Publish the tool version and attach that published version to an agent.
6. Run a read-only action in **Try it**. A `200` result proves transport and tool
   execution; validate the returned business data separately.

Use OAuth client credentials for a workload identity. Use Entra on-behalf-of
only when the MCP server must enforce the signed-in user's identity. Switching
between these profiles does not change the MCP protocol or schema.

## Local neutral conformance server

The optional demo server is built with the official MCP Go SDK and has no SAP
dependency:

```powershell
docker compose -f docker-compose.yml -f docker-compose.mcpdemo.yml up -d --build
```

Register `http://mcpdemo:8090/mcp` with auth type `none`. Discovery must return
`count_words`, `celsius_to_fahrenheit`, `catalog.lookup-v2` and `always_fail`.
They verify plain text, nested input/output schemas, structured content and MCP
tool-error handling. The same checks run offline in `go test ./...`.

## Current SAP-MCP connection

Keep Cosmo's existing Entra sign-in configuration unchanged. For the current
local connection, configure the Cosmo MCP tool as:

```text
Endpoint:   http://host.docker.internal:8000/mcp
Auth type: OAuth 2.0 (client credentials)
Token URL:  https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/token
Client ID:  <existing Cosmo AZURE_AD_CLIENT_ID>
Secret:     <existing Cosmo AZURE_AD_CLIENT_SECRET>
Scope:      api://<existing Cosmo AZURE_AD_CLIENT_ID>/.default
```

Cosmo also needs:

```dotenv
TOOL_EGRESS_ALLOWED_HOSTS=host.docker.internal
```

SAP-MCP remains internal-only and must independently:

- validate issuer, tenant, audience, signature and expiry;
- allow the Cosmo client ID as an application principal (prefer an Entra app
  role for a new production registration; the current allowlist can remain);
- grant that immutable application subject `MCP_CONNECT` plus the required SAP
  data action in its policy store;
- connect to SAP with its own read-only technical credential.

No new Azure application is required for this currently working app-only
connection. Creating/exposing a separate SAP-MCP API application is needed only
when the deployment is intentionally separated from the existing registration
or when user-delegated/OBO authorization is enabled.

## Failure isolation

| Symptom | Boundary to check |
| --- | --- |
| Connection refused or timeout | route, DNS, firewall, internal network and egress allowlist |
| `401` / `403` during discovery | OAuth token URL, audience/scope, app role or allowed client ID |
| Discovery works but an SAP call is denied | SAP-MCP subject policy and business action grant |
| Tool is missing | server `tools/list`, name grammar, pagination and published Cosmo version |
| Input fields are missing | original MCP `inputSchema`; do not infer the contract from editor rows |
| Tool returns `502` with readable content | MCP tool returned `isError: true`; fix the supplied arguments or downstream business condition |

Run the generic conformance test before diagnosing a provider-specific server:

```powershell
cd backend
go test ./internal/tools -run TestMCPConformanceAgainstOfficialSDKServer -v
```

If that passes while SAP-MCP fails, the defect is at the SAP-MCP authentication,
policy, adapter or network boundary—not in Cosmo's generic MCP transport.
