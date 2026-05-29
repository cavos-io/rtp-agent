{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {inherit system;};

        libraryDependencies = with pkgs; [
          ffmpeg
          libogg
          libopus
          onnxruntime
          opusfile
          portaudio
          pkg-config
          rnnoise
        ];

        soExt =
          if pkgs.stdenv.isDarwin
          then "dylib"
          else "so";
      in {
        formatter = pkgs.alejandra;

        devShells.default = pkgs.mkShell {
          buildInputs = [pkgs.go_1_25] ++ libraryDependencies;

          shellHook = ''
            export CGO_ENABLED=1
            export GOTOOLCHAIN=auto

            # Dynamically look up the exact nix store path of onnxruntime and point the env var to its lib directory
            export ORT_SHARED_LIBRARY_PATH="${pkgs.onnxruntime}/lib/libonnxruntime.${soExt}"

            echo "Nix environment activated: Go $(go version) and C libraries loaded."
          '';
        };
      }
    );
}
