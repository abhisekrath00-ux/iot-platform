# Report and KPI expressions

Formulas are used in three places: KPI definitions, computed values, and report highlight rules. All use one parser.

## Syntax

- Numbers: `12`, `0.5`. Arithmetic: `+ - * /` and parentheses.
- Device values: `{device.point}`, for example `{pump1.flow}`. Used in KPIs and computed values.
- Comparisons: `> < >= <= ==`.
- Functions: `abs(x)`, `round(x)`, `sqrt(x)`, `min(a,b)`, `max(a,b)`, `clamp(x,lo,hi)`, `if(condition, then, else)`.
- Conditions inside `if()` can use `and`, `or`, `not` and parentheses.

## Highlight rules on a report

A highlight rule is evaluated once per row (time bucket). If the result is not zero the cell is coloured. Write rules as `if(condition, 1, 0)`.
Row fields, only valid in highlight rules: `{row.avg}`, `{row.min}`, `{row.max}`, `{row.sum}`, `{row.count}`.

Examples:

- Average above 50: `if({row.avg} > 50, 1, 0)`
- Average above 50 and peak below 100: `if({row.avg} > 50 and {row.max} < 100, 1, 0)`
- Either too hot or too cold: `if({row.max} > 80 or {row.min} < 5, 1, 0)`
- Spread wider than 20: `if({row.max} - {row.min} > 20, 1, 0)`
- Too few samples: `if(not({row.count} >= 10), 1, 0)`

The colour is `red` or `amber`. Simple thresholds (above / below) need no formula.

## Errors

A bad formula is refused when saved, with the reason: unknown function, wrong argument count, unbalanced parentheses, or an unknown `{row.x}` field. `{row.*}` in a KPI or computed value is refused.

## Using the AI assistant

Ask the assistant for a formula or a report. It checks the formula first (tool `check_formula`, read only, stores nothing), then proposes `create_report`. You confirm before anything is saved; the server validates again on save. Only admins and operators can create reports. The assistant creates one-metric reports; richer layouts are built in the Reports page.
