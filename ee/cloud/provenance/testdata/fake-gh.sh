#!/usr/bin/env bash
# A test double for the gh CLI. It answers exactly the one invocation
# provenance.VerifyImage makes — `gh attestation verify ...` — and dispatches on
# the image reference, so a test can ask for a valid attestation or a wrong one
# without a network, a registry, or a real signature.
#
# GRAVIX_FAKE_GH_DIR points at the fixture directory; GRAVIX_FAKE_GH_COMMIT is
# substituted into the fixture, so a test can attest a commit it just created in
# a throwaway git repository.
set -u

if [[ "${1:-}" != "attestation" || "${2:-}" != "verify" ]]; then
  echo "fake-gh: unexpected invocation: $*" >&2
  exit 64
fi

fixture="$GRAVIX_FAKE_GH_DIR/gh-attestation-verify-valid.json"
case "${3:-}" in
  *wrong-repo*) fixture="$GRAVIX_FAKE_GH_DIR/gh-attestation-verify-wrong-repo.json" ;;
  *no-attestation*)
    echo "failed to verify attestation: no attestations found" >&2
    exit 1
    ;;
  *not-json*)
    echo "this is not json"
    exit 0
    ;;
esac

sed "s/GRAVIX_VALID_COMMIT/${GRAVIX_FAKE_GH_COMMIT:-GRAVIX_VALID_COMMIT}/g" "$fixture"
