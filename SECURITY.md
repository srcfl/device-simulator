# Security Policy

## Supported versions

Only the latest release of Device Simulator receives security fixes. Always run
the most recent version (the desktop app can self-update).

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, use GitHub's private vulnerability reporting:

1. Go to the **Security** tab of this repository.
2. Click **Report a vulnerability**.
3. Provide a description, reproduction steps, and the affected version.

Alternatively, email **security@sourceful-labs.com**.

We will acknowledge your report within a few business days and keep you updated
on remediation progress. Please give us a reasonable opportunity to address the
issue before public disclosure.

## Scope

Device Simulator is a **testing tool** that intentionally exposes unauthenticated
Modbus, MQTT, and OCPP servers on the local network. It is meant for development
and lab environments and should not be exposed to untrusted networks or the
public internet. Reports about the inherent openness of the simulated protocol
servers are out of scope; reports about unintended data exposure, code execution,
or supply-chain issues are in scope.
