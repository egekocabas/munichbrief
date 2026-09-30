# Parser fixtures

These files are deliberately synthetic. They reproduce only HTML structures
observed on the official Bavarian Police pages linked below; article wording,
people, places, dates, and incident numbers have been invented.

- `bundle_107252_shape.html` — structure observed on article 107252
- `bundle_107292_shape.html` — structure observed on article 107292
- `standalone_107230_shape.html` — structure observed on article 107230
- `standalone_unnumbered_108358_shape.html` — unnumbered single-report page
- `standalone_numbered_headline_shape.html` — article 109114's number only in
  the page headline; its standalone identity must remain unnumbered
- `section_boundaries_shape.html` — mixed sections observed on article 109187,
  with synthetic internal subheadings and a bold paragraph report heading

Release section headings immediately followed by a numbered report in the same
HTML section are excluded from incident bodies. Internal headings followed by
prose, witness appeals, multi-case labels, and ambiguous trailing headings remain
in the body. Full extracted source text still includes release section headings.

The complete official pages must not be copied into this repository. Live
source verification remains opt-in through `MUNICHBRIEF_LIVE_TEST=1`.
