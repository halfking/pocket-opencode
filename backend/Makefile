.PHONY: test-pg

# PG integration tests are opt-in locally. CI must provide the dedicated test DSN.
# POCKET_POSTGRES_DSN remains a compatibility fallback for existing local helpers.
test-pg:
	@if [ -z "$$POCKET_TEST_POSTGRES_DSN" ] && [ -n "$$CI" ]; then \
		echo "ERROR: POCKET_TEST_POSTGRES_DSN must be set in CI" >&2; \
		exit 2; \
	elif [ -z "$$POCKET_TEST_POSTGRES_DSN" ] && [ -z "$$POCKET_POSTGRES_DSN" ]; then \
		echo "SKIP: PostgreSQL integration tests (set POCKET_TEST_POSTGRES_DSN locally)"; \
	else \
		go test ./... -race -count=1; \
	fi
