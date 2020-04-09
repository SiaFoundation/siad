# Windows Gitlab Runner Setup

## GCC setup

Some tests need working, configured GCC.
Easiest way is to use TDM-GCC.

- Go to: https://jmeubank.github.io/tdm-gcc/download/
- Download: `tdm64-gcc-9.2.0.exe` 
- Install: `tdm64-gcc-9.2.0.exe` with default values
- If GCC is not found by tests,
  restart the box to refresh environment variables.

## Gitlab Registration Token Preparation

Gitlab registration token is needed to pair the new runner with Gitlab repository and its CI/CD.

- Open [Sia repo > Settings > CI/CD](https://gitlab.com/NebulousLabs/Sia/-/settings/ci_cd)
- Navigate to `Runners`
- Find: "Use the following registration token during setup:" 
- Copy displayed registration token
- Paste token string to Windows machine to
  `<userhome>\Documents\gitlab-registration-token.txt`
  the file should not contain any spaces or newlines.

## Gitlab Runner Setup

- Login as `Administrator` Windows user
- From Sia repo directory `scripts/gitlab-windows-runner/setup`
  download the installation script `setup-gitlab-windows-runner.ps1`
  to the Windows machine
- Open PowerShell console (do NOT use: "Run as administrator"),
  because it spoils Windows service installation
- Execute the downloaded PowerShell script

The script

- Installs Chocolatey (Windows package/software manager)
- installs git
- Installs golang
- Installs make
- Installs dokany (FUSE for Windows)
- Installs Gitlab Runner
- Registers Gitlab Runner using previously prepared token
- Sets the Gitlab Runner tag to `nebulous-windows`
- Installs Gitlab Runner as a Windows service
- Starts the Gitlab Runner service

Since now the Windows Gitlab runner is ready to execute jobs
that are tagged `nebulous-windows`.