#!/usr/bin/env ruby
# frozen_string_literal: true

formula_path, version, checksums_path = ARGV

if !formula_path || !version || !checksums_path
  warn "usage: update-homebrew-formula.rb FORMULA_PATH VERSION CHECKSUMS_PATH"
  exit 2
end

unless File.file?(formula_path)
  warn "formula not found: #{formula_path}"
  exit 2
end

unless File.file?(checksums_path)
  warn "checksums not found: #{checksums_path}"
  exit 2
end

platforms = %w[
  darwin_amd64
  darwin_arm64
  linux_amd64
  linux_arm64
].freeze

checksums = {}
File.readlines(checksums_path, chomp: true).each do |line|
  hash, name = line.split(/\s+/, 2)
  checksums[name] = hash if hash && name
end

content = File.read(formula_path)

# Normalize only the redundant standalone form; reject ambiguity before writing.
version_lines = content.lines.grep(/\A[ \t]*version[ \t(]/)
if version_lines.length > 1
  warn "multiple version declarations in #{formula_path}"
  exit 1
end
if (version_line = version_lines.first)
  unless version_line.match?(/\A[ \t]*version[ \t]+"[^"\r\n]+"[ \t]*(?:\r?\n)?\z/)
    warn "unsupported version declaration in #{formula_path}"
    exit 1
  end
  content.sub!(version_line, "")
end

platforms.each do |platform|
  filename = "trvl_#{version}_#{platform}.tar.gz"
  sha256 = checksums[filename]
  unless sha256&.match?(/\A[0-9a-f]{64}\z/)
    warn "missing checksum for #{filename}"
    exit 1
  end

  pattern = %r{
    url\ "https://github\.com/MikkoParkkola/trvl/releases/download/v[^/]+/trvl_[^"]+_#{Regexp.escape(platform)}\.tar\.gz"
    \n(\s+)sha256\ "[0-9a-f]{64}"
  }x

  replacement = %Q(url "https://github.com/MikkoParkkola/trvl/releases/download/v#{version}/#{filename}"\n\\1sha256 "#{sha256}")
  unless content.gsub!(pattern, replacement)
    warn "failed to update #{platform} block in #{formula_path}"
    exit 1
  end
end

File.write(formula_path, content)
