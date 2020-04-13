- Refactor the environment variables into the `build` package to address bug
  where `siac` and `siad` could be using different API Passwords. Adds
  `SKYNET_DATA_DIR` environment variable.