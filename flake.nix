{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {inherit system;};

        libraryDependencies = with pkgs; [
          libopus
          onnxruntime
          opusfile
          portaudio
          pkg-config
        ];

        soExt =
          if pkgs.stdenv.isDarwin
          then "dylib"
          else "so";
      in {
        formatter = pkgs.alejandra;

        devShells.default = pkgs.mkShell {
          buildInputs = [pkgs.go_1_26] ++ libraryDependencies;

          shellHook = ''
            export CGO_ENABLED=1
            export ONNXRUNTIME_LIB_PATH="${pkgs.onnxruntime}/lib/libonnxruntime.${soExt}"
          '';
        };
      }
    );
}
