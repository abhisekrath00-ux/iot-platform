# Eastron SDM120 Modbus (single-phase DIN-rail energy meter)

Source: the manufacturer's "SDM120Modbus Smart Meter Modbus Protocol Implementation" V2.4 PDF
(eastroneurope.com/images/uploads/products/protocol/SDM120-MODBUS_Protocol.pdf), fetched 2026-10-20.
Nothing here has been run against a real meter.

## Link

2-wire RS-485, Modbus RTU. Address 1-247 (factory 1; the meter does NOT answer the broadcast
address). Baud selectable 2400/4800/9600+ via holding register 40029 (factory 2400 - note this
differs from the SDM630's 9600). Parity/stop factory one stop bit, no parity (holding register
40019). Floats are 2 registers, most significant register first (`abcd`).

## Measurements (read-only, input registers FC 04, float32 IEEE754, 2 registers each)

| Point | Protocol address | Documented | Unit |
|---|---|---|---|
| voltage | 0 | 30001 | V |
| current | 6 | 30007 | A |
| active_power | 12 | 30013 | W |
| apparent_power | 18 | 30019 | VA |
| reactive_power | 24 | 30025 | VAr |
| power_factor | 30 | 30031 | - |
| frequency | 70 | 30071 | Hz |
| kwh_import, kwh_export | 72, 74 | 30073, 30075 | kWh |
| kvarh_import, kvarh_export | 76, 78 | 30077, 30079 | kvarh |
| w_demand | 84 | 30085 | W |
| max_w_demand | 86 | 30087 | W |
| i_demand | 258 | 30259 | A |
| max_i_demand | 264 | 30265 | A |
| kwh_total | 342 | 30343 | kWh |
| kvarh_total | 344 | 30345 | kvarh |

Units as printed (W/VA/VAr, kvarh lower-case in the source), scale 1. Queries must start on even
registers with an even count (floats are register pairs; an odd query is rejected).

## Edge agent config

```yaml
  - id: sdm120-1
    profile: modbus-generic
    port: /dev/ttyUSB0      # COM3 on Windows
    baud: 2400              # SDM120 factory default (SDM630 ships at 9600)
    data_bits: 8
    stop_bits: 0            # agent default = 1
    parity: none
    address: 1
    interval: 10s
    points:
      - {id: voltage, register: 0, func: 4, type: f32, word_order: abcd, unit: V, min: 0, max: 1000}
      - {id: kwh_import, register: 72, func: 4, type: f32, word_order: abcd, unit: kWh, min: 0, max: 1000000000000}
      # ... full point list in the built-in template "eastron-sdm120"
```

## Not done on purpose

Holding registers 40019/40021/40029 (parity/stop, meter id, baud) and the reset/demand registers
are WRITABLE meter configuration. The agent only reads; configuration writes are not implemented
and would need the platform's approval path.
