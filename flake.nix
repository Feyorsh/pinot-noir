{
  description = "";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { nixpkgs, ... }:
      let
        lib = nixpkgs.lib;
        system = "aarch64-darwin";
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
      in
      {
        apps = {
        };

        devShells.${system}.default = with pkgs; mkShell {
          packages = [
            go
            gopls
          ];
        };
      };
}
