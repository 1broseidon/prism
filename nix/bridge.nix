{ lib, buildGoModule }:

buildGoModule {
  pname = "prism-bridge";
  version = "0.1.0";
  src = lib.cleanSource ./..;
  subPackages = [ "cmd/prism-bridge" ];
  vendorHash = null;
  CGO_ENABLED = 0;
  ldflags = [ "-s" "-w" ];
}
