# In-app guides (Resources page)

The Resources page holds guides that work offline, with search and categories: first day, connecting a sensor through an edge box, how Modbus data gets names and units, choosing the server address, safe rollouts, System health and Dev tools, why control needs two people, the assistant, and a glossary. They are plain data in `web/src/lib/guides.ts`, so they ship with the web app and need no internet.

Each guide says what is built and what has not been run on real hardware. Tests check unique ids, that every link points at a real page, that search works, and that no guide claims "production-ready".

Not built: screenshots or video inside guides, per-role guides, translated guides, links from each page to its guide.
