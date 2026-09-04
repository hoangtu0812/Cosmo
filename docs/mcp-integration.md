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
   `none`, bearer, custom header, OAuth 2.0 client credentials, provider-neutral
   Authorization Code + PKCE, or the legacy Microsoft Entra on-behalf-of adapter.
4. Save, then select **Discover MCP tools**. Review the discovered descriptions
   and schemas before publishing.
5. Publish the tool version and attach that published version to an agent.
6. Run a read-only action in **Try it**. A `200` result proves transport and tool
   execution; validate the returned business data separately.

Use OAuth client credentials for a workload identity. Use Authorization Code +
PKCE when the MCP server must enforce the signed-in user's identity. That flow
discovers RFC 9728 protected-resource metadata and RFC 8414/OIDC authorization
server metadata; it does not depend on the provider used to sign in to Cosmo.
The Entra OBO profile remains only for existing integrations that explicitly
need token exchange.

## Provider-neutral user authorization

For an OAuth-protected MCP server:

1. Set `PUBLIC_API_URL` to the browser-reachable Cosmo backend origin. The
   callback is `<PUBLIC_API_URL>/api/tools/oauth/callback`.
2. Select **OAuth 2.1 (Authorization Code + PKCE)** and click **Inspect OAuth
   server**. Cosmo reads the MCP resource and authorization-server metadata.
3. Register the displayed callback with the selected provider. Enter the
   provider-issued Client ID; Client secret is optional for public clients.
4. Enter explicit space-separated scopes only when the provider requires a
   provider-qualified value. Otherwise leave Scope blank and Cosmo uses the
   protected-resource metadata.
5. Save, then click **Connect my account**. Each Cosmo user completes their own
   authorization. Access and refresh tokens are encrypted and keyed by both
   tool and user.
6. After the callback reports connected, discover the MCP tools, publish them,
   install the tool, and enable **Callable in chat**.

Cosmo always sends PKCE S256 and validates state. It verifies the authorization
response issuer when supplied (and requires it when the provider advertises
RFC 9207 support). A server without standards metadata, or one that explicitly
advertises only incompatible PKCE methods, is refused. Providers that support
PKCE but omit the optional discovery field remain compatible. A
shared tool shares only its client registration and contract; it never shares
one user's token with another user.

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

To test the provider-neutral user flow against the current Entra-backed
SAP-MCP, add this redirect URI to the chosen Entra application:

```text
http://localhost:8080/api/tools/oauth/callback
```

Then select **OAuth 2.1 (Authorization Code + PKCE)** in Cosmo. Use the Entra
application's Client ID and secret and, because Entra expects a qualified API
scope, enter:

```text
api://<SAP-MCP application ID>/access_as_user offline_access
```

The application must expose `access_as_user` and the signing-in user must be
allowed by SAP-MCP policy. This registration work is an identity-provider
requirement, not a dependency between Cosmo and SAP-MCP.

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
