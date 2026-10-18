{
  description = "clai - vendor-neutral AI agent for the terminal";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = "unstable-${self.shortRev or self.dirtyShortRev or "dirty"}";
    in
    {
      packages = forAll (pkgs: rec {
        clai = pkgs.buildGoModule {
          pname = "clai";
          inherit version;
          src = ./.;
          vendorHash = "sha256-KYXVEASUjx+u0aq3ftMeKqRJ3/Xwizwcz10bpfh3BkE=";
          ldflags = [
            "-s"
            "-w"
            "-X"
            "github.com/baalimago/clai/internal/version.BuildVersion=${version}"
          ];
          nativeBuildInputs = [ pkgs.installShellFiles ];
          # The suite is not sandbox-safe: it shells out to npm, git and ripgrep
          # through PATH, hard-codes /bin/sh and /bin/cat, and resolves the
          # config dir through HOME. CI owns the test gate (validate.yml runs
          # the race suite three times), so this build proves the packaging.
          doCheck = false;
          postInstall = ''
            installShellCompletion --cmd clai \
              --bash <($out/bin/clai completion bash) \
              --zsh <($out/bin/clai completion zsh)
          '';
          meta = {
            description = "Vendor-neutral AI agent and Go library for the terminal";
            homepage = "https://github.com/baalimago/clai";
            license = pkgs.lib.licenses.mit;
            mainProgram = "clai";
            platforms = pkgs.lib.platforms.unix;
          };
        };
        default = clai;
      });

      apps = forAll (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.clai}/bin/clai";
        };
      });

      formatter = forAll (pkgs: pkgs.nixfmt);
    };
}
