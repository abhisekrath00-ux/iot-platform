# AI features: design and honest labels

Status: design plus what exists. Every feature carries one label:
- **Statistical**: fixed maths, deterministic, explainable, no training.
- **Learned**: a model fitted on tenant data or a pretrained model.
- **LLM**: a language model, external client or local, can be wrong.

## Built today
| Feature | Label | Notes |
|---|---|---|
| N-sigma deviation rule | Statistical | Mean and standard deviation of the point's recent history; off until a rule exists |
| Seasonal baseline rule | Statistical | Same UTC hour of day (or hour of week) over 3 to 28 days, N-sigma; needs 30 samples at that hour. Integration-tested on synthetic data; UTC only, no per-site timezone yet |
| KPI outside user band | Statistical | Fixed band |
| Outlier card on device page | Statistical | Median and MAD, can disagree with the sigma rule |
| Holt-Winters forecast with 95% interval and backtest (`GET /v1/telemetry/forecast`, device page card) | Statistical | Season 24h on hourly averages; shown as a prediction only if it beats repeating yesterday by 5% on the last 24h; refuses when under 80% of hours have data; tested on synthetic data, not on real plant data |
| CUSUM level-shift detection (in the forecast response) | Statistical | Run on the series after removing the hour-of-day profile; fixed limit h=8, so it can still raise a false alarm; no alert rule uses it yet |
| Related signals (`GET /v1/telemetry/related`) | Statistical | Lagged Pearson correlation among points of the same device; says "correlated with", never "caused by"; same device only, not across assets yet |
| Forecast-limit alert rule (`forecast_limit`) | Statistical | Alert when the forecast crosses a limit within 1-72h; checked every 15 min by one replica; only fires if the backtest beat repeating yesterday; off until a rule is created |
| MCP: 7 read tools, 2 flow-draft tools, `forecast_time_series`, `related_signals` (12 tools total, none actuate) | LLM client, deterministic tools | The model lives in the user's client; tools only read, tenant-scoped |

No learned models and no free-form natural-language query tool exists yet (the MCP client's own model chooses among validated tools). Not built: cross-device and cross-asset root-cause ranking.

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


## Status: alert root-cause hints (built, statistical, tested locally)

`GET /v1/alerts/{id}/root-cause` and the MCP tool `root_cause_hints` rank points on the alert's device,
and on other devices of the same asset, by lagged Pearson correlation of hourly averages with the
alerting point over 1-30 days (default 7) ending at the hour the alert was raised. The point comes from
the alert's rule, or from `point_id` when the rule does not name one.

- Label `statistical`. No model, no LLM, nothing is written, nothing actuates.
- Wording is "correlated with" and "moved earlier / later", never "caused by". The response carries
  the data window and how many series were compared.
- Needs 80% of the window's hours for the alerting point, otherwise `enough_data` is false and no
  hints are returned. Candidates need |r| >= 0.6, at most 60 series compared, top 10 returned, lags
  of up to 6 h either way.
- Limits: hourly resolution only, linear relationships only, no check for a shared third cause, no
  seasonality removal, so two daily cycles will correlate. Treat hints as places to look.
- Tested with synthetic series (known lead and lag, an uncorrelated series, tenant isolation, missing
  data). Not evaluated on real plant data.

## Status: level-shift detection tool (built, statistical, tested locally)

MCP tool `detect_level_shifts` runs CUSUM on the hourly average of one point after removing the daily
cycle (baseline: first 48 h, restarts after each shift) and returns when it shifted, with the mean
before and after (24 h each side). It works without a usable forecast, unlike the change points on
the forecast endpoint. Label `statistical`. It does not say why: process change, recalibration and a
failing sensor look the same. Needs 3 or more days of hourly data. Tested on synthetic data with one
known step and for tenant isolation; not evaluated on real data.
