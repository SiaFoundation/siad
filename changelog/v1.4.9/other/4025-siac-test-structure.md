- Do not store reference to siac testing httpClient in global variable,
  store it within test local variable.
- Rename global variable `httpClient` to `siacGlobalHttpClient`.