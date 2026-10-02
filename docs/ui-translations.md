# UI translation integrity

Interface messages live in `internal/web/locales/active.<language>.toml`.
The `Translation integrity` GitHub Actions job runs on every pull request,
push to `main`, and manual CI run. It runs independently of the full application
checks, so an incomplete catalog has its own visible failure.

Run the same checks locally from the repository root:

```sh
go test ./internal/translationcheck ./cmd/check-translations
go run ./cmd/check-translations
```

The checker exits with a nonzero status when it finds an error. Diagnostics
identify the relevant file, message key, or language so the problem can be fixed
before merging. It uses the committed source and catalogs without contacting
translation services or the production database.

## What is checked

Checks follow this order so malformed input is caught before completeness and
usage are evaluated:

1. Parse the catalogs and confirm that every registered UI language has a
   nonempty catalog. Reject invalid TOML, duplicate keys, unsupported message
   fields, incorrect value types, blank translations, and malformed message
   templates. Unregistered catalog files and invalid UTF-8/NFC text also fail.
2. Read translation references from Go and HTML template syntax trees. Literal
   message keys are discovered from source. Dynamic lookups must have an
   explicit, finite contract in the checker; unresolved lookups and stale
   contracts fail. Messages with no source use are reported as unnecessary.
3. Check that every required message key is present in every language. A key
   added to one catalog cannot silently fall back to another language.
4. Compare named placeholders against the canonical catalog's `other` form
   (currently German), rejecting missing or extra placeholders in every plural
   form, including the canonical language's own plural forms. Languages may use
   different plural forms; they do not need identical plural-category sets.

These checks verify structure and coverage. They cannot decide whether a
translation is idiomatic, accurate, or accidentally copied from another
language. Translated copy still needs human review.

## Adding or changing a message

Add the message's source reference and its translation to every registered
language in the same change. Keep the message key and named placeholders
consistent across catalogs; localize the surrounding text and use plural forms
appropriate to each language. Run the checker before pushing.

Messages use plain root placeholders such as `{{.Count}}` and `{{.Page}}`.
Conditional logic, computed field access, custom delimiters, and nested
templates are rejected so placeholder coverage remains explicit. Use go-i18n's
plural forms for language-specific plural selection.

Prefer literal translation keys. If the application genuinely needs a dynamic
key, extend its finite source contract in `internal/translationcheck` and add a
test covering that contract. The contract should explain which source values
can reach the lookup, rather than broadly exempting a family of keys from
unused-message checks.

When removing a feature or renaming a message, remove the old key from every
catalog and update any affected dynamic contract. Do not retain unused messages
as a way to suppress a missing translation or source-usage error.

When registering a new UI language, add a complete catalog and verify all
messages with this check. Public report translations and their processing
pipeline are separate; this check does not inspect or modify LLM prompts.
