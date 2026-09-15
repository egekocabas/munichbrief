# Translation without losing the place

German is the canonical presentation. Each other language gets its own job,
model choice, and publication result. Translation receives the accepted German
title and summary, never the retained original report.

## Protect, translate, restore

1. Find known place names in the German text.
2. Replace them with typed tokens such as `__MB_STREET_0001__`.
3. Ask the model to translate while preserving those tokens.
4. Check their spelling and occurrence counts, then restore the original names.
5. Validate the translated fields before saving them.

For example, a street name stays exactly as written in German while the
sentence around it changes language. Missing, duplicated, or invented tokens
cause the result to be rejected.

## Where the names come from

| Source | Names used | Attribution |
| --- | --- | --- |
| Munich GeodatenService | Streets and districts | Landeshauptstadt München, dl-de/by-2.0 |
| GeoNames | Munich-area places | GeoNames, CC BY 4.0 |
| OpenStreetMap | Roads, places, transit, landmarks | © OpenStreetMap contributors, ODbL 1.0 |

A separate SQLite database stores validated generations. A failed refresh
keeps the last valid generation. Without a valid matcher, new translations
pause while German processing and the website remain available.

## Languages

The [language registry](../internal/languages/languages.go) connects each route
code with its locale, labels, formatting, and translation identity. Localized
UI text lives in [TOML catalogues](../internal/web/locales/active.en.toml).

Language support also needs a versioned prompt, complete UI messages, suitable
fonts, and a matching public ingress prefix. Adding a language does not
implicitly translate the historical archive.

## Models and retries

- Ollama runs separately; the application image includes no model weights.
- Adapters handle different model request formats.
- Jobs retain the model, adapter, and prompt version used for that attempt.
- A failed replacement leaves the earlier successful result available when it
  still matches the current German presentation.
- Validation catches structural problems; fluent human review is still needed
  to judge meaning, tone, and uncertainty.

See [processing internals](../internal/processing/README.md) and
[licensing](licensing-review.md) for more detail.
