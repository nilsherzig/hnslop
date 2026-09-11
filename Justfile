set shell := ["bash", "-euo", "pipefail", "-c"]

extension_source := "extension"
extension_artifacts := "extension-artifacts"
extension_package := "hnslop.xpi"
web_ext := env_var_or_default("WEB_EXT", "web-ext")

# Show the available development and release recipes.
default:
    @just --list

# Run the Go test suite.
test:
    go test ./...

# Build the server binary.
build:
    go build ./...

# Build the unsigned Firefox package and update the package served by the server.
extension-rebuild:
    rm -rf {{extension_artifacts}}
    mkdir -p {{extension_artifacts}}
    {{web_ext}} build --source-dir {{extension_source}} --artifacts-dir {{extension_artifacts}} --filename {{extension_package}} --overwrite-dest
    cp {{extension_artifacts}}/{{extension_package}} {{extension_package}}

# Submit the extension to AMO for an unlisted signed build and update the served package.
# Set WEB_EXT_API_KEY and WEB_EXT_API_SECRET before running this recipe.
extension-sign:
    #!/usr/bin/env bash
    set -euo pipefail
    api_key="${WEB_EXT_API_KEY:-}"
    api_secret="${WEB_EXT_API_SECRET:-}"
    test -n "$api_key" || { echo "WEB_EXT_API_KEY is required" >&2; exit 1; }
    test -n "$api_secret" || { echo "WEB_EXT_API_SECRET is required" >&2; exit 1; }
    rm -rf {{extension_artifacts}}
    mkdir -p {{extension_artifacts}}
    {{web_ext}} sign --source-dir {{extension_source}} --artifacts-dir {{extension_artifacts}} --channel unlisted --api-key "$api_key" --api-secret "$api_secret"
    signed_package="$(find {{extension_artifacts}} -maxdepth 1 -type f -name '*.xpi' -print -quit)"
    test -n "$signed_package" || { echo "web-ext did not produce a signed XPI" >&2; exit 1; }
    cp "$signed_package" {{extension_package}}

# Validate the extension without producing an artifact.
extension-lint:
    {{web_ext}} lint --source-dir {{extension_source}}
