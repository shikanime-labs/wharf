# Flake-parts module: declare the images of a repository once, get a
# `nix run .#build-<name>` app per image and the `skaffold.yaml` CI consumes.
#
#   imports = [ inputs.wharf.flakeModule ];
#   apps.wharf.automata = {
#     image = "ghcr.io/shikanime-labs/machines/automata";
#   };
#
#   nix run .#build-automata        # build + push
#   nix build .#skaffold-yaml       # generated build artifacts
{ wharf }:

{ config, lib, ... }:

let
  inherit (lib)
    concatMap
    concatStringsSep
    escapeShellArg
    literalExpression
    mapAttrs'
    mkOption
    nameValuePair
    optionalString
    types
    ;

  # The artifact command skaffold runs, also the single source of truth for
  # the wrapper the build app exposes. Flake refs never contain spaces, so the
  # YAML stays unquoted for skaffold's own command splitter.
  buildCommand = cfg: "wharf skaffold build --flake ${cfg.flake} --option accept-flake-config=true";
in

{
  options.apps.wharf = mkOption {
    description = "OCI images built from this flake by wharf.";
    default = { };
    type = types.attrsOf (
      types.submodule (
        { name, ... }:
        {
          options = {
            image = mkOption {
              type = types.str;
              description = "Destination image reference, tag included.";
            };

            flake = mkOption {
              type = types.str;
              default = ".#${name}-oci-image";
              defaultText = literalExpression ''".#''${name}-oci-image"'';
              description = "Flake URL whose package attribute holds the image.";
            };

            platforms = mkOption {
              type = types.listOf types.str;
              default = [ ];
              description = "Target platforms in os/arch form; empty means the host platform.";
            };

            push = mkOption {
              type = types.bool;
              default = true;
              description = "Push the built image to its registry.";
            };
          };
        }
      )
    );
  };

  config.perSystem =
    { pkgs, ... }:
    let
      cli = wharf.packages.${pkgs.system}.default;
      artifacts = lib.attrValues config.apps.wharf;
    in
    {
      apps = mapAttrs' (
        name: cfg:
        nameValuePair "build-${name}" {
          type = "app";
          program = toString (
            pkgs.writeShellScript "build-${name}" ''
              args=(
                build
                --image ${escapeShellArg cfg.image}
                --flake ${escapeShellArg cfg.flake}
                --option accept-flake-config=true
              )
              ${optionalString (
                cfg.platforms != [ ]
              ) "args+=(--platforms ${escapeShellArg (concatStringsSep "," cfg.platforms)})"}
              ${optionalString cfg.push "args+=(--push)"}
              exec ${cli}/bin/wharf "''${args[@]}" "$@"
            ''
          );
        }
      ) config.apps.wharf;

      packages.skaffold-yaml = pkgs.writeText "skaffold.yaml" (
        concatStringsSep "\n" (
          [
            "apiVersion: skaffold/v4beta11"
            "kind: Config"
            "build:"
            "  artifacts:"
          ]
          ++ concatMap (cfg: [
            "    - image: ${cfg.image}"
            "      custom:"
            "        buildCommand: ${buildCommand cfg}"
          ]) artifacts
        )
        + "\n"
      );
    };
}
