# Purpose

Generate a GoLang application that guards the .env file in a project

## Problem:

The .env file often carries project specific secrets. We need to guard them. But the problem is inconvenience of doing so. People want everything related to the project in one place. That is necessary, considering the number of open projects that people work on. So we commit the secrets into github, but in an encrypted form.

## Features

- The utility makes sure that the .env is included in the .gitignore
- it is triggered just before a commit, to encrypt and delete the .env file and then restores it again
- it is triggered just after a clone/pull - so that the user always sees the .env file in place
- The secret is stored in the folder ~/.guardenv/secretname - this name may vary per project, so that there is no clash if same person works with two different teams

## Code requirements

- Golang application that can encrypts/decrypts files
- The script should check for the secret file name within the repository - .guardenv/config, then pick the secret from the ~/.guardenv/secretfile .... identify the guarded.env files all over the repository - based on the config and encrypt them - this should be triggered automatically on a commit
- The decryptor should again look for the guarded.env files based on the .guardenv/config .. and based on the secret. and decrypt them into .env file
- The guardenv init command should identify the .env files in the project (ignoring the paths that are skipped by .gitignore) and build the .guardenv/config file within the project. It should generate the secret with unique file name and save that to the ~/.guardenv folder.
