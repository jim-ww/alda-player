{
  description = "Drop-in replacement for Alda's JVM player, in pure Go";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      inherit (nixpkgs) lib;
      forAllSystems = lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      # nixpkgs' alda is a symlinkJoin of the Go client and the JVM player.
      # Swap the player in that join only: the client keeps its store path
      # (so it comes from the binary cache) and the JVM player is never
      # built or fetched.
      withPlayers =
        alda: players:
        alda.overrideAttrs (old: {
          paths = builtins.filter (p: !lib.hasInfix "-alda-player-" (toString p)) old.paths ++ players;
          meta = old.meta // {
            sourceProvenance = [ lib.sourceTypes.fromSource ];
          };
        });

      playerFor = pkgs: alda: pkgs.callPackage ./package.nix { aldaVersion = alda.version; };
    in
    {
      overlays.default = final: prev: {
        alda-player = playerFor final prev.alda;
        alda = withPlayers prev.alda [ final.alda-player ];
      };

      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          alda-player = playerFor pkgs pkgs.alda;
        in
        {
          inherit alda-player;
          alda = withPlayers pkgs.alda [ alda-player ];
          default = alda-player;
        }
      );

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              # The client only; the end-to-end test builds its own player.
              (withPlayers pkgs.alda [ ])
            ];
            ALDA_SOUNDFONT = "${pkgs.soundfont-fluid}/share/soundfonts/FluidR3_GM2-2.sf2";
          };
        }
      );
    };
}
