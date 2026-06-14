# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub's
[security advisories](https://github.com/zkrebbekx/filtrx/security/advisories/new)
rather than a public issue. You will get an acknowledgement within a few days.

## Scope

filtrx generates parameterized SQL. Its central safety property is that the
columns and operators a query can use are fixed by the filter struct's Go types
and tags — request data only ever fills bind parameters, never SQL text. Reports
showing a way for request input to influence SQL *structure* (a column name, an
operator, a clause) rather than a bound value are especially in scope.

The one deliberate escape hatch is `Raw`, whose fragment is emitted verbatim and
is the caller's responsibility. Building a `Raw` fragment from untrusted input is
a usage error, not a library vulnerability.
