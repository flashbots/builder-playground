@custom_recipes.md
@custom_recipes_guide.md

# Rules and suggestions

These suggestions apply everywhere including outside of this section.

Refrain from reading the builder-playground codebase directly to avoid context bloat and long thinking cycles.

If there is an ambiguity about how to solve a specific problem about any of the service software and it requires delving deep, state the problem back to the user and prompt to choose a direction. While going in the chosen direction, prefer smaller steps in reasoning and solving.

Stick to the most optimistic and the fastest path while figuring out the details and working on the implementations. Save troubleshooting to the debugging phase.

Do not run exploratory commands to resolve further ambiguity. Ask the user immediately if you have understood the potential problem solving direction to continue with, well enough.

If you notice that you are going in circles while thinking after two iterations, prompt the user with a few directions unless stated otherwise by the user.

Never parallelize recipe generation and file reads - work on each item sequentially.

You already have `builder-playground` installed in the host system. Verify with `which builder-playground`.

See CLI with `builder-playground --help`. You can also do that on the subcommands.

The `playground.yaml` file can be ran like `builder-playground start playground.yaml` and with `--detached` flag if needed. That will already exit the process with 0 if everything runs successfully.

When making debugging and troubleshooting, avoid running multiple sessions at once unless requested otherwise by the user. The `--detached` flag causes a concurrent session to live in the background. While that happens, a new session will have different (increased) port numbers so please stop any running session first and then start the investigation.

If you did not run with `--detached`, do not expect for the playground to finish running if no timeout was specified. Verify the output and interrupt if needed.

# User request

First, consider if the user request fits in any of the requests outlined below.

If not, discover the right method and make the best effort to help the user.

## Recipe generation

All custom recipes are created on top of a base recipe. Discover available base recipes with the `recipes` command.

The user should specify which base recipe they want to use, as part of the request. If it is ambiguous, prompt the user to select a base recipe from the available ones.

When the user asks to generate a recipe YAML, you have two options:
- Write a recipe file from scratch
- Use the `generate` command to first generate the base recipe YAML and modify to remove unmodified services/components and add new ones

The request can contain special requests about having multiple services of same type or a specific configuration in the new or existing services. When considering these type of requests, base problem solving on the existing usage of service software (e.g. EL/CL), try to follow the same patterns and discover and correctly use the unused capabilities to satisfy the request.

If there is ambiguity about how many components to add, of which type, and which components to pair with which etc. ask for clarification immediately and avoid thinking about it for long time.

Prefer keeping healthmon services if they don't make anything impractical.

Add any missing ready checks even if there is a concern about that being extra.

Ports specified with `{{Port ...}}` is available from Docker networking so don't worry about using them in the recipe.

Host binaries need unique port defaults — just pick non-overlapping numbers. Docker containers don't conflict internally.

Rbuilder TOML (or other config file) hardcodes must match the {{Port}} defaults you chose in YAML. When writing stuff to the config file, pick it from the YAML file to use matching values (e.g. for ports).

If the reference recipe (rbuilder/bin) doesn't override el's args/volumes and works, the playground handles path translation. Trust it. Don't re-derive — just follow the same pattern for new instances.

### Consensus clients

To help with consensus client peering:
```yaml
consensus_clients:
  lighthouse:
    flag: --libp2p-addresses
    multiple_peers: repeat flag
    peer_id_required: false
    dns4_supported: true
    example: /dns4/beacon/tcp/9000

  prysm:
    flag: --peer
    multiple_peers: repeat flag
    peer_id_required: true
    dns4_supported: true
    example: /dns4/beacon/tcp/9000/p2p/16Uiu2HAm...

  teku:
    flag: --p2p-static-peers
    multiple_peers: comma-separated
    peer_id_required: true
    dns4_supported: false
    example: /ip4/192.168.1.1/tcp/9000/p2p/16Uiu2HAm...

  nimbus:
    flag: --direct-peer
    multiple_peers: repeat flag
    peer_id_required: true
    dns4_supported: false
    example: /ip4/192.168.1.1/tcp/9000/p2p/16Uiu2HAm...

  grandine:
    flag: --libp2p-addresses
    multiple_peers: unknown
    peer_id_required: unknown
    dns4_supported: unknown
    example: unknown  # accepts multiaddr but format requirements unclear
```

And you can use the DNS available for the container (like `beacon`) from within other containers in the same playground bridge network.

Add and use an EL per CL if not explicitly stated otherwise.

### `replace_args` cannot add new flags
Only patches existing flag-value pairs. To add a flag not in the base, use `args` (full replacement).

### Port allocation: TOML can't use templates
`{{Port}}` and `{{Service}}` only work in YAML `args`. TOML values like `cl_node_url` must be hardcoded to match the YAML defaults. Coordinate manually.

### Volume naming convention
Local volumes (`is_local: true`) become `volume-<service_name>-<volume_name>` in the session dir. E.g. service `el2` with volume name `data` → `volume-el2-data`. rbuilder TOML paths follow this: `reth_datadir = "volume-el2-data"`.

### Docker vs host port conflicts
Host binaries share the host network — second instances must use different port defaults. Docker containers have isolated namespaces so internal ports don't conflict.

### EL-per-CL pairing
Each beacon needs its own reth (SKILL.md rule). Full dependency chain: beacon2→el2, rbuilder2→(el2 + beacon2). Add healthmon sidecars for any new service depended on with `:healthy`.

### rbuilder

If the host is Linux, then reth and rbuilder can only be ran as host processes together with the recipe. This does not apply to op-rbuilder.

Make rbuilders, in their TOML config, depend on the consensus clients based on the startup order.

# References

IMPORTANT: EXECUTE BELOW IN STRICT ORDER, ONE AT A TIME, NEVER IN PARALLEL, NEVER WITH LONG THINKING CYCLES. REPORT EXPLICITLY AFTER FINISHING EACH STEP QUICKLY AND SAVE THINKING FOR LATER.

read: builder-playground generate l1 --stdout
read: builder-playground generate opstack --stdout
read: builder-playground generate buildernet --stdout
generate: builder-playground generate rbuilder/bin
read: rbuilder.toml
read: playground.yaml
