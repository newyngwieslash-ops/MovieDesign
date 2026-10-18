# Bundled runtime dependencies

| Component | Fixed version | Official source | License |
| --- | --- | --- | --- |
| OpenCode Windows x64 CLI | 1.19.0 | https://github.com/anomalyco/opencode/releases/tag/v1.19.0 | MIT, Copyright (c) 2025 opencode |
| Official MCP Go SDK | 1.4.1 | https://github.com/modelcontextprotocol/go-sdk/tree/v1.4.1 | MIT |
| AI SDK OpenAI / OpenAI-compatible / Anthropic providers | included in the fixed OpenCode executable | https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/provider/provider.ts | Apache-2.0 |

`scripts/build-opencode.ps1` downloads only the fixed official `opencode-windows-x64.zip`, verifies its published SHA256, extracts the CLI and copies it to the application's `externalBin`. No global installation is read or modified.

- Archive SHA256: `17108e9448c4fdad48eb8714c4ed6f5dfc9cf86c2eb8f2ce5d02043fef806eb5`
- Executable SHA256: `bb731f311416df16691fa16763eb87657c4a5f5f3bcff5202718c416e8f7ab1a`
- Official archive: https://github.com/anomalyco/opencode/releases/download/v1.19.0/opencode-windows-x64.zip
- Official asset digest: https://github.com/anomalyco/opencode/releases/expanded_assets/v1.19.0
- License: https://github.com/anomalyco/opencode/blob/v1.19.0/LICENSE

OpenCode's `BUNDLED_PROVIDERS` contains the three supported SDK imports; settings cannot supply npm/package/plugin names. Source verification establishes bundling, while the native test establishes the pinned executable accepts the custom provider configuration. Real inference remains unexecuted without an application-configured connection and user approval.

The official MCP security-fixed version requires Go 1.25.0. This repository already uses Go 1.25.8; SDK installation also upgrades its required `golang.org/x/sys` to v0.40.0. The SDK is pinned here for M5.2 registration; M5.1 does not claim an MCP tools/list or agent tool roundtrip.

OpenCode MIT notice (distributed with this application):

Copyright (c) 2025 opencode

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

Pinned source references: [raw interpolation](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/config/variable.ts#L30-L38), [configuration discovery](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/config/config.ts), [owned global roots](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/core/src/global.ts), [effective agent permissions](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/agent/agent.ts), [SDK Go requirement](https://github.com/modelcontextprotocol/go-sdk/blob/v1.4.1/go.mod), and [SDK advisory / fixed 1.4.1](https://github.com/modelcontextprotocol/go-sdk/security/advisories/GHSA-89xv-2j6f-qhc8). These are source facts, separate from the saved live `/doc` schema and opt-in runtime checks in `gateway/internal/agent/runtime_test.go`.
