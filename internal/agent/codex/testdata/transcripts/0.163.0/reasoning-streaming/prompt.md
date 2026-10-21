---
model: gpt-5.6-luna
reasoning_effort: medium
reasoning_summary: detailed
---
Think carefully through the boolean condition `if (!items.length && isEnabled)`.

1. Explain exactly when it evaluates to true and false.
2. Enumerate the behavior for these cases:
   - items is []
   - items is [1]
   - isEnabled is true
   - isEnabled is false
3. Point out any confusing or error-prone aspects of relying on `!items.length`.
4. Compare it with:
   - `if (items.length === 0 && isEnabled)`
   - `if (!items?.length && isEnabled)`
5. Conclude with which version is safest in production code and why.

Think step by step before giving the final answer.
