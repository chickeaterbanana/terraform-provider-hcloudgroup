# Security Policy

## Supported Versions

Only the latest `0.x` release receives security fixes.

## Reporting a Vulnerability

Please use [GitHub Private Security Advisories](https://github.com/chickeaterbanana/terraform-provider-hcloudgroup/security/advisories/new) to report vulnerabilities confidentially. Do not open public issues for security bugs.

We will make a best-effort attempt to acknowledge your report within 30 days.

## Verifying Releases

All release artifacts are signed with the following GPG key:

```
E8CCF925766517EC1E99A9F9444DC818EC36F233
```

Verify a release archive:

```sh
gpg --keyserver keyserver.ubuntu.com --recv-keys E8CCF925766517EC1E99A9F9444DC818EC36F233
gpg --verify terraform-provider-hcloudgroup_<version>_checksums.txt.sig \
            terraform-provider-hcloudgroup_<version>_checksums.txt
```
