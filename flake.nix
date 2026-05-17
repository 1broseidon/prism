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
      });
}
