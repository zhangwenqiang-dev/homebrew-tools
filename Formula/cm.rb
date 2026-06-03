class Cm < Formula
  desc "SSH, VNC, and rsync profile manager"
  homepage "https://github.com/zhangwenqiang-dev/homebrew-tools"
  url "https://github.com/zhangwenqiang-dev/homebrew-tools.git",
      tag: "v0.1.9"
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", "-o", bin/"cm", "./cmd/cm"
  end

  test do
    assert_match "Usage:", shell_output("#{bin}/cm --help")
  end
end
