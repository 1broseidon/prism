---
id: task-9
title: Nix-based minimal container images for bridge runtimes
description: |-
  Replace the fat bridge Dockerfile (node + python + bash in one image) with Nix-built minimal images per runtime profile. Each spawned backend container has only the bridge binary and the exact runtime it needs — nothing else.

  ## Context

  Task-8 delivers per-container isolation: each MCP backend runs in its own Docker container. But every container uses the same fat image (~200MB) containing node, python, bash, and all their transitive dependencies. A compromised MCP server has access to tools and libraries it doesn't need.

  Nix solves this by building minimal container images from composable closures. A node-only image contains the bridge binary, nodejs, npm, and ca-certs — no python, no bash, no coreutils. A python-only image contains the bridge binary, python, uv, and ca-certs — no node.

  This is a security property, not just a size optimization: if it's not in the container, it can't be exploited.

  ## Nix Flake Structure

  ```
  prism/
  ├── flake.nix          — top-level flake
  ├── flake.lock         — pinned nixpkgs
  └── nix/
      ├── bridge.nix     — prism-bridge Go binary derivation
      └── images.nix     — container image definitions per runtime profile
  ```

  ### flake.nix
  ```nix
  {
    inputs = {
      nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
      flake-utils.url = "github:numtide/flake-utils";
    };

    outputs = { self, nixpkgs, flake-utils }:
      flake-utils.lib.eachDefaultSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          bridge = pkgs.callPackage ./nix/bridge.nix {};
          images = pkgs.callPackage ./nix/images.nix { inherit bridge; };
        in {
          packages = {
            inherit bridge;
            image-base = images.base;
            image-node = images.node;
            image-python = images.python;
            image-full = images.full;
          };
        }
      );
  }
  ```

  ### nix/bridge.nix — Go binary
  ```nix
  { lib, buildGoModule }:
  buildGoModule {
    pname = "prism-bridge";
    version = "0.1.0";
    src = ./..;
    subPackages = [ "cmd/prism-bridge" ];
    vendorHash = "...";  # computed from go.sum
    CGO_ENABLED = 0;
    ldflags = [ "-s" "-w" ];  # strip debug info for minimal size
  }
  ```

  ### nix/images.nix — Runtime profiles
  ```nix
  { pkgs, bridge, lib }:
  let
    # Common base: bridge binary + CA certs
    baseContents = [
      bridge
      pkgs.cacert
    ];

    mkImage = { name, tag ? "latest", contents ? [], extraConfig ? {} }:
      pkgs.dockerTools.buildLayeredImage ({
        inherit name tag;
        contents = baseContents ++ contents;
        config = {
          Entrypoint = [ "${bridge}/bin/prism-bridge" ];
          ExposedPorts = { "3001/tcp" = {}; };
        } // extraConfig;
      });
  in {
    # Base: bridge binary only. For Go-based MCP servers or pre-compiled binaries.
    base = mkImage {
      name = "prism-bridge";
      tag = "base";
    };

    # Node: bridge + Node.js + npm. For npx-based MCP servers.
    node = mkImage {
      name = "prism-bridge";
      tag = "node";
      contents = [ pkgs.nodejs_22 ];
    };

    # Python: bridge + Python + uv. For uvx/pip-based MCP servers.
    python = mkImage {
      name = "prism-bridge";
      tag = "python";
      contents = [ pkgs.python312 pkgs.uv ];
    };

    # Full: bridge + Node + Python + bash. Equivalent to current fat Dockerfile.
    # Use as fallback or when operators don't know the runtime ahead of time.
    full = mkImage {
      name = "prism-bridge";
      tag = "full";
      contents = [ pkgs.nodejs_22 pkgs.python312 pkgs.uv pkgs.bash ];
    };
  }
  ```

  ## Image Sizes (estimated)

  | Profile  | Contents                          | Estimated Size |
  |----------|-----------------------------------|----------------|
  | base     | bridge + ca-certs                 | ~15 MB         |
  | node     | bridge + ca-certs + nodejs + npm  | ~80 MB         |
  | python   | bridge + ca-certs + python + uv   | ~65 MB         |
  | full     | bridge + everything + bash        | ~160 MB        |

  Compare to current Dockerfile: ~200MB (alpine + node + python + bash + apk metadata).

  ## Runtime Selection in Bridge Manager

  The bridge manager (task-8) needs to know which image to use when spawning a container. Two approaches:

  ### Approach A: Auto-detect from command
  The bridge inspects the command and selects the image:
  - `npx`, `node`, `npm` → `prism-bridge:node`
  - `uvx`, `python`, `python3`, `pip` → `prism-bridge:python`
  - Everything else → `prism-bridge:full` (safe fallback)

  ```go
  func (d *DockerRuntime) imageForCommand(command string) string {
      base := filepath.Base(command)
      switch base {
      case "npx", "node", "npm", "yarn", "pnpm", "bunx", "bun":
          return d.images["node"]
      case "uvx", "uv", "python", "python3", "pip", "pip3":
          return d.images["python"]
      default:
          return d.images["full"]
      }
  }
  ```

  ### Approach B: Explicit in spawn request
  The spawn request includes a `runtime` hint:
  ```json
  {
    "id": "github",
    "command": "npx",
    "args": ["@modelcontextprotocol/server-github"],
    "runtime": "node"
  }
  ```

  **Recommendation: Approach A with optional override.** Auto-detect covers 90% of cases. The operator can override via the `runtime` field in the spawn request if auto-detection is wrong.

  ### Bridge config for images
  ```
  prism-bridge manage \
    --runtime docker \
    --image-base prism-bridge:base \
    --image-node prism-bridge:node \
    --image-python prism-bridge:python \
    --image-full prism-bridge:full
  ```

  Or via env vars:
  ```
  BRIDGE_IMAGE_BASE=prism-bridge:base
  BRIDGE_IMAGE_NODE=prism-bridge:node
  BRIDGE_IMAGE_PYTHON=prism-bridge:python
  BRIDGE_IMAGE_FULL=prism-bridge:full
  ```

  ## Admin UI Changes

  The admin UI's "Add Server" form can optionally show a runtime selector when `bridge_url` is configured:
  - Auto (default) — bridge auto-detects from command
  - Node — force node runtime
  - Python — force python runtime
  - Full — use full image

  This is a minor UI addition to the existing form. The selector is only shown when Prism has a bridge configured.

  ## CI/CD: Building Images

  ### GitHub Actions
  ```yaml
  - name: Build Nix images
    run: |
      nix build .#image-base .#image-node .#image-python .#image-full
      # Each produces a Docker image tarball in ./result
      docker load < result  # for each image
  ```

  ### Local development
  ```bash
  # Build all images
  nix build .#image-node
  docker load < result

  # Or build and load in one step (convenience script)
  ./scripts/build-images.sh
  ```

  ## Migration from Dockerfile

  The existing `cmd/prism-bridge/Dockerfile` is kept as a fallback for users who don't have Nix. It continues to produce the "full" image. The Nix images are the recommended path for production.

  Eventually the Dockerfile could be replaced entirely, but that's not a goal for this phase.

  ## Files to create/modify

  ### New files
  - `flake.nix` — top-level Nix flake
  - `nix/bridge.nix` — Go binary derivation
  - `nix/images.nix` — container image definitions per runtime profile
  - `scripts/build-images.sh` — convenience script for building and loading images

  ### Modified files
  - `cmd/prism-bridge/runtime_docker.go` — image selection logic (auto-detect + override)
  - `cmd/prism-bridge/manage.go` — accept --image-* flags, pass to DockerRuntime
  - `cmd/prism-bridge/main.go` — new flags in usage text
  - `internal/admin/ui.html` — optional runtime selector in Add Server form (only when bridge configured)

  ## Test Plan
  - Nix build: `nix build .#image-node` produces a valid Docker image
  - Image contents: node image contains nodejs but NOT python; python image contains python but NOT nodejs
  - Auto-detection: `npx` → node image, `uvx` → python image, `./my-binary` → full image
  - Override: explicit `runtime: "python"` in spawn request uses python image regardless of command
  - Integration: spawn with node image → npx MCP server starts → tools work
  - Integration: spawn with python image → uvx MCP server starts → tools work
  - Regression: Dockerfile still builds and works (fallback path)
priority: medium
tags:
  - bridge
  - containers
  - nix
  - security
  - phase-3
dependsOn:
  - task-8
relatedFiles:
  - cmd/prism-bridge/Dockerfile
  - cmd/prism-bridge/runtime_docker.go
  - cmd/prism-bridge/manage.go
  - cmd/prism-bridge/main.go
  - internal/admin/ui.html
createdAt: "2026-03-27T09:30:00.000Z"
contract:
  status: draft
  deliverables:
    - type: file
      path: flake.nix
      description: Top-level Nix flake with image outputs
    - type: file
      path: nix/bridge.nix
      description: prism-bridge Go binary derivation
    - type: file
      path: nix/images.nix
      description: Container image definitions per runtime profile
    - type: file
      path: scripts/build-images.sh
      description: Convenience script for building and loading images
    - type: file
      path: cmd/prism-bridge/runtime_docker.go
      description: Image selection logic with auto-detect and override
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - nix build .#image-base .#image-node .#image-python .#image-full
  constraints:
    - Node image must NOT contain python; python image must NOT contain node
    - Auto-detect covers npx/node/npm → node and uvx/python/pip → python
    - Full image is the safe fallback for unknown commands
    - Existing Dockerfile preserved as non-Nix fallback
    - Nix images use buildLayeredImage for efficient Docker layer caching
    - All images share the same bridge binary — only runtime deps differ
completedAt: "2026-03-27T20:07:47.610Z"
updatedAt: "2026-03-27T20:07:47.610Z"
---

## Description
Replace the fat bridge Dockerfile (node + python + bash in one image) with Nix-built minimal container images per runtime profile. Each spawned backend container has only the bridge binary and the exact runtime it needs. This is a security property: if it's not in the container, it can't be exploited.

## Key Insight
Nix makes multiple minimal images trivially maintainable. Without Nix, you'd need separate Dockerfiles or multi-stage builds per runtime. With Nix, runtime profiles are composable closures — adding a new profile is a few lines in images.nix.
