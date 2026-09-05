# frozen_string_literal: true

require "json"
require "yaml"

repository_root = File.expand_path("..", __dir__)
workflow = YAML.safe_load(
  File.read(File.join(repository_root, ".github/workflows/release.yml")),
  aliases: true
)
ci_workflow = YAML.safe_load(
  File.read(File.join(repository_root, ".github/workflows/ci.yml")),
  aliases: true
)
prepare_workflow = YAML.safe_load(
  File.read(File.join(repository_root, ".github/workflows/prepare-release.yml")),
  aliases: true
)
steps = workflow.dig("jobs", "release", "steps")
raise "release job is missing" unless steps
ci_steps = ci_workflow.dig("jobs", "test", "steps")
raise "CI test job is missing" unless ci_steps
prepare_steps = prepare_workflow.dig("jobs", "prepare", "steps")
raise "prepare-release job is missing" unless prepare_steps

commands = steps.map { |step| step["run"] }.compact.join("\n")
uses = steps.map { |step| step["uses"] }.compact

required = [
  "refs/tags/v${version}",
  "release-dependencies.json",
  "DEVICE_VERSION",
  "DEVICE_WORKFLOW",
  "device_proxy.release_workflow",
  "Agent-Remote/agent-remote-device",
  "sha256sum --check",
  "govulncheck@v1.6.0",
  'any(.[]; has("finding")) | not',
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

device_checkouts = steps.select do |step|
  step["uses"]&.start_with?("actions/checkout@") &&
    step.dig("with", "repository") == "Agent-Remote/agent-remote-device"
end
raise "release workflow does not check out the pinned device tag" unless device_checkouts.any? { |step| step.dig("with", "ref") == "v${{ steps.device.outputs.version }}" }
raise "release workflow does not verify the pinned device commit" unless commands.include?("DEVICE_COMMIT")
raise "release workflow does not compare managed skills" unless commands.include?("scripts/check-managed-skills.sh")

ci_commands = ci_steps.map { |step| step["run"] }.compact.join("\n")
managed_skill_source = JSON.parse(
  File.read(File.join(repository_root, "managed-skill-source.json"))
)
raise "managed skill source schema is invalid" unless managed_skill_source["schema_version"] == 1
raise "managed skill source repository is invalid" unless managed_skill_source["repository"] == "Agent-Remote/agent-remote-device"
raise "managed skill source commit is invalid" unless managed_skill_source["commit"]&.match?(/\A[0-9a-f]{40}\z/)
ci_device_checkout = ci_steps.any? do |step|
  step["uses"]&.start_with?("actions/checkout@") &&
    step.dig("with", "repository") == "Agent-Remote/agent-remote-device" &&
    step.dig("with", "ref") == "${{ steps.managed-skill.outputs.commit }}"
end
raise "CI does not check out the pinned managed skill source" unless ci_device_checkout
raise "CI does not resolve the managed skill source" unless ci_commands.include?("managed-skill-source.json")
raise "CI does not verify the managed skill source commit" unless ci_commands.include?("git -C .external/agent-remote-device rev-parse HEAD")
raise "CI does not compare managed skills" unless ci_commands.include?("scripts/check-managed-skills.sh")
raise "CI does not reject Go vulnerability findings" unless ci_commands.include?('any(.[]; has("finding")) | not')
ci_text = File.read(File.join(repository_root, ".github/workflows/ci.yml"))
raise "CI does not cancel superseded runs" unless ci_text.include?("concurrency:")
raise "CI jobs do not have timeouts" unless ci_text.include?("timeout-minutes:")
raise "CI does not filter documentation-only changes" unless ci_text.include?("dorny/paths-filter@v4.0.3")
raise "CI coverage upload must not depend on Codecov availability" unless ci_text.include?("fail_ci_if_error: false")
raise "CI still repeats the full privileged Go test suite" if ci_text.include?("go test -covermode=atomic -coverprofile=coverage.out ./...")
raise "CI does not run the focused privileged integration test" unless ci_text.include?("TestDialNetworkNamespaceLoopbackIntegration")
quality_script = File.read(File.join(repository_root, "scripts/run-quality-checks.sh"))
raise "quality checks cannot publish a requested coverage profile" unless quality_script.include?('COVERAGE_PROFILE:-')

prepare_commands = prepare_steps.map { |step| step["run"] }.compact.join("\n")
prepare_device_checkout = prepare_steps.any? do |step|
  step["uses"]&.start_with?("actions/checkout@") &&
    step.dig("with", "repository") == "Agent-Remote/agent-remote-device" &&
    step.dig("with", "ref") == "v${{ steps.device.outputs.version }}"
end
raise "prepare-release does not check out the pinned device tag" unless prepare_device_checkout
raise "prepare-release does not resolve the pinned dependency" unless prepare_commands.include?("release-dependencies.json")
raise "prepare-release does not compare managed skills" unless prepare_commands.include?("scripts/check-managed-skills.sh")
prepare_check_index = prepare_steps.index { |step| step["name"] == "Verify managed skill matches tagged device source" }
prepare_tag_index = prepare_steps.index { |step| step["name"] == "Commit and tag" }
raise "prepare-release managed skill check must run before tagging" unless prepare_check_index && prepare_tag_index && prepare_check_index < prepare_tag_index

raise "release workflow does not generate an SBOM" unless uses.any? { |value| value.start_with?("anchore/sbom-action@") }
raise "release workflow does not attest provenance" unless uses.any? { |value| value.start_with?("actions/attest-build-provenance@") }
raise "release workflow does not publish the SBOM signature" unless File.read(File.join(repository_root, ".github/workflows/release.yml")).include?(".spdx.json.sigstore.json")
raise "release checksums must contain asset basenames" unless commands.include?('(cd dist && sha256sum "$archive_name"')

dockerfile = File.read(File.join(repository_root, "Dockerfile"))
raise "Docker build must copy the locked Go dependency checksums" unless dockerfile.include?("COPY go.mod go.sum ./")
