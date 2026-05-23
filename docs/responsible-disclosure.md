# Responsible Disclosure Policy

## Reporting Security Vulnerabilities

Gravix takes security seriously. If you discover a vulnerability, please report it responsibly.

### How to Report

- **Email:** security@gravix.io
- **Encrypt** your report using our PGP key (available at `/.well-known/pgp-key.txt`)
- Include a detailed description, reproduction steps, and potential impact

### What to Include

1. Type of vulnerability (e.g., injection, authentication bypass, data exposure)
2. Steps to reproduce
3. Affected endpoints or components
4. Potential impact assessment
5. Any suggested fix (optional)

### Our Commitment

- **Acknowledgment:** Within 2 business days
- **Triage:** Within 5 business days
- **Resolution:** Critical issues within 7 days, others within 30 days
- **Disclosure:** Coordinated disclosure after fix is deployed (90-day maximum)

### Scope

**In scope:**
- Gravix API endpoints (ingestion, gateway)
- Dashboard web application
- SDK libraries (Go, Node, Python, Java)
- Authentication and authorization
- Data handling and storage

**Out of scope:**
- Denial of service attacks
- Social engineering
- Physical attacks
- Third-party services (Stripe, cloud providers)
- Self-hosted instances with modified code

### Safe Harbor

We will not pursue legal action against researchers who:
- Act in good faith and follow this policy
- Do not access, modify, or delete data belonging to other users
- Report findings promptly and do not publicly disclose before coordinated disclosure
- Do not use automated scanning tools that generate excessive traffic

### Recognition

With your permission, we will acknowledge your contribution on our Security Hall of Fame page.
