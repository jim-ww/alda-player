{
  lib,
  stdenv,
  buildGoModule,
  makeBinaryWrapper,
  alsa-lib,
  soundfont-fluid,
  # Must equal the alda client's version; see main.go.
  aldaVersion,
  soundfont ? "${soundfont-fluid}/share/soundfonts/FluidR3_GM2-2.sf2",
}:

buildGoModule {
  pname = "alda-player";
  version = aldaVersion;

  # Only Go sources, so that editing anything else doesn't rebuild.
  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      ./third_party
      (lib.fileset.fileFilter (f: f.hasExt "go") ./.)
    ];
  };

  vendorHash = "sha256-sv1VjVAZIqZj/ZIW3L7lEAgcVibGJq4U2+dk37G2gfs=";

  # A separate module, used through a replace directive.
  excludedPackages = [ "third_party" ];

  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${aldaVersion}"
  ];

  nativeBuildInputs = [ makeBinaryWrapper ];
  checkFlags = [ "-short" ];
  __darwinAllowLocalNetworking = true;

  # oto talks to PulseAudio/PipeWire in pure Go and loads libasound only as
  # a fallback.
  postInstall = ''
    wrapProgram $out/bin/alda-player \
      --set-default ALDA_SOUNDFONT ${soundfont} ${lib.optionalString stdenv.hostPlatform.isLinux "--suffix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ alsa-lib ]}"}
  '';

  meta = {
    description = "Drop-in replacement for Alda's JVM player, in pure Go";
    license = lib.licenses.gpl3Plus;
    mainProgram = "alda-player";
    platforms = lib.platforms.unix;
  };
}
