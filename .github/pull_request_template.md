## Summary

- What changed and why?
- Which behavior or contract is affected?

## Validation

- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] Frontend assets are current (`npm ci && npm run build`)
- [ ] Helm checks pass when deployment files change
- [ ] Documentation is updated when public behavior changes

## Safety

- [ ] No credentials, databases, real article copies, generated model output,
      or personal deployment values are included
- [ ] Privacy, attribution, and fail-closed presentation rules remain intact

The pull-request title must follow Conventional Commits.
