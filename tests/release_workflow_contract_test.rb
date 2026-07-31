# frozen_string_literal: true

require "yaml"

repository_root = File.expand_path("..", __dir__)
workflow = YAML.safe_load(
  File.read(File.join(repository_root, ".github/workflows/release.yml")),
  aliases: true
)
steps = workflow.dig("jobs", "release", "steps")
raise "release job is missing" unless steps

commands = steps.map { |step| step["run"] }.compact.join("\n")
uses = steps.map { |step| step["uses"] }.compact

required = [
  "refs/tags/v${version}",
  "Agent-Remote/agent-remote-device",
  "sha256sum --check",
  "govulncheck@v1.6.0",
  "$report.sha256",
  "$report.sigstore.json",
  "$sbom.sigstore.json",
  "cosign verify-blob",
  "gh attestation verify",
  "DEVICE_PROXY_DIR",
].freeze
required.each do |fragment|
  raise "release workflow is missing #{fragment}" unless commands.include?(fragment)
end

raise "release workflow does not generate an SBOM" unless uses.any? { |value| value.start_with?("anchore/sbom-action@") }
raise "release workflow does not attest provenance" unless uses.any? { |value| value.start_with?("actions/attest-build-provenance@") }
raise "release workflow does not publish the SBOM signature" unless File.read(File.join(repository_root, ".github/workflows/release.yml")).include?(".spdx.json.sigstore.json")
