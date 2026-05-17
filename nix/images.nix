{ pkgs, bridge, lib }:
let
  baseContents = [ bridge pkgs.cacert ];

  mkImage = { name, tag ? "latest", contents ? [ ], extraConfig ? { } }:
    pkgs.dockerTools.buildLayeredImage {
      inherit name tag;
      contents = baseContents ++ contents;
      config = {
        Entrypoint = [ "${bridge}/bin/prism-bridge" ];
        ExposedPorts = { "3001/tcp" = { }; };
      } // extraConfig;
    };
in {
  base = mkImage {
    name = "prism-bridge";
    tag = "base";
  };

  node = mkImage {
    name = "prism-bridge";
    tag = "node";
    contents = [ pkgs.nodejs_22 ];
  };

  python = mkImage {
    name = "prism-bridge";
    tag = "python";
    contents = [ pkgs.python312 pkgs.uv ];
  };

  full = mkImage {
    name = "prism-bridge";
    tag = "full";
    contents = [ pkgs.nodejs_22 pkgs.python312 pkgs.uv pkgs.bash ];
  };
}
