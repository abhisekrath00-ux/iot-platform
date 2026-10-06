# Register maps: how raw Modbus data gets names and units

Modbus carries numbers at register addresses, nothing else. A register map (a sensor profile) says what each register means: id, register, function code, data type, word order, scale, unit, and a valid range.

## Where maps come from
1. Built-in templates (Selec MX300 and MFM383A, from their manuals). Profiles page: "Load a built-in template".
2. Import: CSV (header row with `id,register,func,type,word_order,scale,unit,min,max`, any column order, `id` and `register` required) or JSON (array of points or an object with `points`). File upload or paste.
3. Build by hand in the same form. Export CSV or JSON to share or back up a map.

Nothing is saved on import. Points load into the form, the page lists problems with row numbers, and Create profile stays disabled until they are fixed. A person always confirms the labels.

## Checks
Unique ids, register 0-65535 with the 1 or 2 register width of the type, no overlapping registers on the same function code, valid function (1-4), bool only on coils and discrete inputs, known word order, non-zero scale, and max greater than min (the range is how bad readings are rejected).

## Status
Built and unit-tested (parsing, round trip, each check). Screenshot-checked in headless Chrome. Not built: the dashboard flow for edge auto-detect suggestions (the edge agent can suggest a template from a scan, nothing in the UI shows it yet), the assistant drafting a map from a datasheet, a larger library of devices. Non-Modbus raw protocols (serial, BLE, CAN) still use the older point form without import.
