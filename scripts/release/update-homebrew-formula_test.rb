# frozen_string_literal: true

require "minitest/autorun"
require "open3"
require "rbconfig"
require "tmpdir"

class UpdateHomebrewFormulaTest < Minitest::Test
  SCRIPT = File.expand_path("update-homebrew-formula.rb", __dir__)
  PLATFORMS = %w[darwin_amd64 darwin_arm64 linux_amd64 linux_arm64].freeze

  def test_updates_version_urls_and_all_checksums
    with_fixture do |formula, checksums|
      _stdout, stderr, status = Open3.capture3(
        RbConfig.ruby, SCRIPT, formula, "1.21.0", checksums
      )

      assert status.success?, stderr
      content = File.read(formula)
      assert_includes content, 'version "1.21.0"'
      PLATFORMS.each_with_index do |platform, index|
        filename = "trvl_1.21.0_#{platform}.tar.gz"
        expected_pair = %r{
          url\ "https://github\.com/MikkoParkkola/trvl/releases/download/v1\.21\.0/#{Regexp.escape(filename)}"
          \n\s+sha256\ "#{(index + 1).to_s * 64}"
        }x
        assert_match expected_pair, content
      end
    end
  end

  def test_inserts_version_when_formula_has_none
    with_fixture(include_version: false) do |formula, checksums|
      _stdout, stderr, status = Open3.capture3(
        RbConfig.ruby, SCRIPT, formula, "1.21.0", checksums
      )

      assert status.success?, stderr
      content = File.read(formula)
      assert_includes content, "license \"PolyForm-Noncommercial-1.0.0\"\n  version \"1.21.0\"\n"
    end
  end

  def test_missing_platform_checksum_fails_without_rewriting_formula
    with_fixture(omit: "linux_arm64") do |formula, checksums|
      before = File.read(formula)
      _stdout, stderr, status = Open3.capture3(
        RbConfig.ruby, SCRIPT, formula, "1.21.0", checksums
      )

      refute status.success?
      assert_includes stderr, "missing checksum for trvl_1.21.0_linux_arm64.tar.gz"
      assert_equal before, File.read(formula)
    end
  end

  private

  def with_fixture(omit: nil, include_version: true)
    Dir.mktmpdir("trvl-homebrew-formula-test") do |dir|
      formula = File.join(dir, "trvl.rb")
      checksums = File.join(dir, "checksums.txt")
      File.write(formula, formula_fixture(include_version: include_version))
      File.write(checksums, checksum_fixture(omit: omit))
      yield formula, checksums
    end
  end

  def formula_fixture(include_version: true)
    blocks = PLATFORMS.map do |platform|
      %(      url "https://github.com/MikkoParkkola/trvl/releases/download/v1.20.0/trvl_1.20.0_#{platform}.tar.gz"\n) \
        + %(      sha256 "#{'0' * 64}"\n)
    end.join("\n")
    version_line = include_version ? "  version \"1.20.0\"\n" : ""

    <<~RUBY
      class Trvl < Formula
        license "PolyForm-Noncommercial-1.0.0"
      #{version_line}#{blocks}
      end
    RUBY
  end

  def checksum_fixture(omit:)
    lines = []
    PLATFORMS.each_with_index do |platform, index|
      next if platform == omit

      lines << "#{(index + 1).to_s * 64}  trvl_1.21.0_#{platform}.tar.gz"
    end
    lines.join("\n")
  end
end
