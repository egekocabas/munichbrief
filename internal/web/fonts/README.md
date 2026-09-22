# Embedded social-card fonts

These variable font binaries are embedded only for server-rendered social
cards whose scripts are not covered by Go's bundled fonts:

- `NotoSansSC-VF.ttf` comes from the Google Fonts
  [`ofl/notosanssc`](https://github.com/google/fonts/tree/main/ofl/notosanssc)
  family and covers Simplified Chinese.
- `NotoSansDevanagari-VF.ttf` comes from the Google Fonts
  [`ofl/notosansdevanagari`](https://github.com/google/fonts/tree/main/ofl/notosansdevanagari)
  family and covers Hindi shaping and conjuncts.

Both use weight 700 for headlines and 400 for subtitles and are distributed under
the SIL Open Font License 1.1 in [`OFL.txt`](OFL.txt). Replacing either binary changes the social-card
renderer version and therefore its cache identity. Keep the filenames stable,
verify the required glyph and shaping tests, and rebuild the application after
an upstream font update.
