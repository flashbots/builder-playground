# Custom Recipes

> **Important:** This is yet an experimental feature and sometimes might not always work as intended. Please submit issues with your use cases if you find any problems.

Custom recipes let you build on top of existing base recipes (like `l1`, `opstack`, or `buildernet`) by adding, removing, or modifying components and services. Instead of duplicating entire configurations, you write a small YAML file that describes only the changes you want to make.

## How Custom Recipes Work

A custom recipe is a YAML file with two main parts:

- **`base`**: The name of the base recipe to extend (e.g., `l1`)
- **`recipe`**: A map of components and services to add, modify, or remove

The playground merges your customizations with the base recipe at runtime. You can:

- Change container images and versions
- Add new services or components
- Remove existing services or components
- Override arguments, environment variables, and configuration files

## Quick Start: Writing a Simple Custom Recipe

Let's create a custom recipe that uses a specific version of reth. Create a file called `playground.yaml`:

```yaml
base: l1

recipe:
  reth:
    services:
      el:
        tag: v1.9.0
```

This tells the playground to start with the `l1` recipe but override the `el` service in the `reth` component to use tag `v1.9.0`.

You can also change the image entirely:

```yaml
base: l1

recipe:
  reth:
    services:
      el:
        image: ghcr.io/my-org/my-reth-fork-image
        tag: v1.9.0
```

## Running Your Custom Recipe

Once you have a `playground.yaml` file, run it with:

```bash
playground start playground.yaml
```

The playground will load the base recipe, apply your modifications, and start all the services.

---

# Browsing Available Recipes

To see all available base recipes and pre-built custom recipes:

```bash
playground recipes
```

This shows:

- **Base Recipes**: Core recipes like `l1`, `opstack`, `buildernet`
- **Custom Recipes**: Pre-built configurations like `rbuilder/bin` and `rbuilder/custom`

Each recipe displays a description and the components it includes.

## Running a Pre-Built Custom Recipe

You can run any custom recipe directly by name:

```bash
playground start rbuilder/bin
```

This is equivalent to generating the custom recipe files and running them when you don't need to modify the recipe.

## Generating a Custom Recipe

If you want to inspect or modify a pre-built custom recipe, generate it to your current directory:

```bash
playground generate rbuilder/bin
```

This creates:

- `playground.yaml` - The recipe configuration
- Any additional files the recipe needs (e.g., `rbuilder.toml`)

You can then edit these files and run:

```bash
playground start playground.yaml
```

---

# Generating a Full Base Recipe

Sometimes you want to see the complete configuration of a base recipe, modify it extensively, and run your own version. You can generate the full YAML representation of any base recipe:

```bash
playground generate l1
```

This creates a `playground.yaml` file with the complete `l1` recipe, showing all components, services, images, arguments, and configuration.

Then edit `playground.yaml` to make any changes you need, and run it:

```bash
playground start playground.yaml
```

This approach gives you full control over every aspect of the recipe while still benefiting from the playground's orchestration.
