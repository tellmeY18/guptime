{
  description = "A development shell for the guptime Go project";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          # The packages available in the development environment
          buildInputs = with pkgs; [
            go
            gopls
            go-swag
            delve
            sqlite
          ];
        };
      });
}
