.PHONY: test-pg

# PG integration tests are opt-in locally. They use POCKET_TEST_POSTGRES_DSN
# first and fall back to POCKET_POSTGRES_DSN, matching the test helpers.
# CI supplies POCKET_TEST_POSTGRES_DSN from its postgres service.
test-pg:
	@if [ -z "$$POCKET_TEST_POSTGRES_DSN" ] && [ -z "$$POCKET_POSTGRES_DSN" ]; then \
		echo "SKIP: PostgreSQL integration tests (set POCKET_TEST_POSTGRES_DSN or POCKET_POSTGRES_DSN)"; \
	else \
		go test ./internal/redclaw/... ./internal/server/... -race -count=1; \
	fi
