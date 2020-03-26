# Windows Gitlab Runner Setup

## Gitlab Registration Token Preparation

Gitlab registration token is needed to pair the new runner with Gitlab repository and its CI/CD.

- Open [Sia repo > Settings > CI/CD](https://gitlab.com/NebulousLabs/Sia/-/settings/ci_cd)
- Navigate to `Runners`
- Find: Use the following registration token during setup: 
- Copy displayed registration token
- Paste token string to Windows machine to
  `<userhome>\Documents\gitlab-registration-token.txt`
  the file should not contain any spaces or newlines.

## Gitlab Runner - Windows Service User

Gitlab runner Windows service user should be run under `Administrator` account to have access to needed files.

To prepare Windows service to be run under this user:

- Type Administrator user password to Windows machine to
  `<userhome>\Documents\gitlab-registration-token.txt`
  the file should not contain any spaces or newlines.
  

## Gitlab Runner Setup

- From Sia repo directory `scripts/gitlab-windows-runner/setup`
  copy the installation script `(run-as-admin)-setup-gitlab-windows-runner.ps1`
  to the Windows machine
- Run the PowerShell script as Admin

The script

- Installs Chocolatey (Windows package/software manager)
- Installs Gitlab Runner
- Registers Gitlab Runner using previously prepared token
- Sets the Gitlab Runner tag to `windows10`
- Installs Gitlab Runner as a Windows service
- Starts the Gitlab Runner service

Since now the Windows Gitlab runner is ready to execute jobs.