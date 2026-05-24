class Jerry < Formula
  desc "Jerry programming language compiler"
  homepage "https://github.com/jeffscottbrown/jerry-lang"
  url "URL_PLACEHOLDER"
  sha256 "SHA256_PLACEHOLDER"
  license "MIT"
  head "https://github.com/jeffscottbrown/jerry-lang.git", branch: "main"

  def install
    # Build the C runtime static archive.
    arch_flag = Hardware::CPU.arm? ? ["-arch", "arm64"] : ["-arch", "x86_64"]
    lib.mkpath
    system ENV.cc, "-O2", *arch_flag, "-c", "runtime/src/runtime.c",
           "-Iruntime/src", "-o", "jerry_runtime.o"
    system "ar", "rcs", lib/"jerry_runtime.a", "jerry_runtime.o"

    # Install stdlib .jer files.
    (pkgshare/"stdlib").install Dir["stdlib/*.jer"]

    env = {
      "JERRY_RUNTIME" => (lib/"jerry_runtime.a").to_s,
      "JERRY_STDLIB"  => (pkgshare/"stdlib").to_s,
    }

    # Bootstrap jerry-compiler directly from the checked-in LLVM IR.
    # self-host/bootstrap.ll is generated from the self-hosted compiler and
    # is included in the source tarball — no seed binary download needed.
    target_flag = Hardware::CPU.arm? ? [] : ["-target", "x86_64-apple-darwin"]
    system ENV.cc, *target_flag, "-O0", "self-host/bootstrap.ll",
           lib/"jerry_runtime.a", "-o", "jerry-compiler", "-lm"

    # Build all Jerry tools using the freshly bootstrapped compiler.
    with_env(env) do
      system "./jerry-compiler", "cmd/jerry-test/",   "-o", "jerry-test"
      system "./jerry-compiler", "cmd/jerry-create/", "-o", "jerry-create"
      system "./jerry-compiler", "cmd/jerry-sweep/",  "-o", "jerry-sweep"
      system "./jerry-compiler", "cmd/jerry-get/",    "-o", "jerry-get"
      system "./jerry-compiler", "cmd/jerry-lsp/",    "-o", "jerry-lsp"

      File.write("cmd/jerry-main/version.jer",
        "fn jerry_version(): string { return \"#{version}\"; }")
      system "./jerry-compiler", "cmd/jerry-main/", "-o", "jerry-native"
      File.write("cmd/jerry-main/version.jer",
        "fn jerry_version(): string { return \"dev\"; }")
    end

    bin.install "jerry-native" => "jerry"
    bin.install "jerry-compiler"
    bin.install "jerry-test"
    bin.install "jerry-create"
    bin.install "jerry-sweep"
    bin.install "jerry-lsp"
    bin.install "jerry-get"
  end

  test do
    ENV["JERRY_RUNTIME"]  = (lib/"jerry_runtime.a").to_s
    ENV["JERRY_STDLIB"]   = (pkgshare/"stdlib").to_s
    ENV["JERRY_COMPILER"] = (bin/"jerry-compiler").to_s
    ENV["JERRY_TEST"]     = (bin/"jerry-test").to_s
    ENV["JERRY_CREATE"]   = (bin/"jerry-create").to_s
    ENV["JERRY_SWEEP"]    = (bin/"jerry-sweep").to_s
    ENV["JERRY_LSP"]      = (bin/"jerry-lsp").to_s
    ENV["JERRY_GET"]      = (bin/"jerry-get").to_s

    assert_match version.to_s, shell_output("#{bin}/jerry --version")

    (testpath/"hello.jer").write <<~EOS
      fn main() {
        print("Hello from Homebrew!");
      }
    EOS
    assert_match "Hello from Homebrew!", shell_output("#{bin}/jerry run hello.jer")
  end
end
