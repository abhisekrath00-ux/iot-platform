# AI features: design and honest labels

Status: design plus what exists. Every feature carries one label:
- **Statistical**: fixed maths, deterministic, explainable, no training.
- **Learned**: a model fitted on tenant data or a pretrained model.
- **LLM**: a language model, external client or local, can be wrong.

## Built today
| Feature | Label | Notes |
|---|---|---|
| N-sigma deviation rule | Statistical | Mean and standard deviation of the point's recent history; off until a rule exists |
| KPI outside user band | Statistical | Fixed band |
| Outlier card on device page | Statistical | Median and MAD, can disagree with the sigma rule |
| Holt-Winters forecast with 95% interval and backtest (`GET /v1/telemetry/forecast`, device page card) | Statistical | Season 24h on hourly averages; shown as a prediction only if it beats repeating yesterday by 5% on the last 24h; refuses when under 80% of hours have data; tested on synthetic data, not on real plant data |
| CUSUM level-shift detection (in the forecast response) | Statistical | Run on the series after removing the hour-of-day profile; fixed limit h=8, so it can still raise a false alarm; no alert rule uses it yet |
| Related signals (`GET /v1/telemetry/related`) | Statistical | Lagged Pearson correlation among points of the same device; says "correlated with", never "caused by"; same device only, not across assets yet |
| MCP server, 7 read-only tools | LLM client, deterministic tools | The model lives in the user's client; tools only read, tenant-scoped |

No learned models and no natural-language query tools beyond the 7 existing MCP tools exist yet. Not built: seasonal-baseline alert rules, forecast-based alert rules, cross-device and cross-asset root-cause ranking.

## Planned, in order
1. **Seasonal baseline** (Statistical): per-point hour-of-week profile, alert
   on deviation from the expected value for that hour, not the global mean.
   Removes false alarms on daily and weekly cycles.
2. **Forecast** (Statistical): Holt-Winters style exponential smoothing with
   prediction intervals, shown with its interval and backtest error. Used for
   "will this cross a limit in N hours". Labelled statistical, not AI-magic.
3. **Change-point detection** (Statistical): CUSUM for slow drift and level shifts.
4. **Root-cause hints** (Statistical): when an alert fires, rank sibling
   points under the same asset by lagged correlation with the alerting point
   over the window and by which moved first. Output reads "correlated with,
   moved earlier", never "caused by".
5. **Natural-language query** (LLM): extend MCP with query tools that take
   validated parameters (point, range, aggregation, asset). The model picks
   tools; it never writes SQL. Answers cite the tool call and range used.
6. **On-prem model option** (LLM): the product talks to any OpenAI-compatible
   chat endpoint set by the admin (for example a local llama.cpp or Ollama
   server). Off by default, no model bundled, no data leaves the site unless
   the admin points it elsewhere. Quality depends on the model chosen.
7. **Learned detector** (Learned, optional, last): an isolation-forest style
   multivariate detector trained per tenant on their own history, with
   precision measured on labelled alerts before it can be turned on. Only built
   if the statistical set above misses real cases in testing.

## Rules
- Anything that creates alerts is off until a user creates the rule.
- Every AI output shows its label and the data window it used.
- Control actions are never taken from AI output; the approval and four-eyes
  path is unchanged.
- No accuracy claim without a test on recorded data that is committed.
