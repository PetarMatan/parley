# orders-service

A small order API used as the review subject of the Parley testbed. Standard library only.

| Call | Effect |
|---|---|
| `POST /orders` | Creates an order from `{customer, lines[{sku, quantity, unit_cents}]}`; 201 with the priced order, 400 on a validation error |
| `GET /orders` | Lists orders, oldest first; `?customer=` filters |
| `GET /orders/{id}` | One order with its quote; 404 if unknown |
| `POST /orders/{id}/cancel` | Cancels an open order; 409 if already cancelled |

Limits: customer up to 80 characters, 1 to 100 lines, quantity 1 to 1000, unit price 1 cent to 100 000.00. Quantity discounts: 5 % from 10 units, 10 % from 50 units per line. Tax is 20 % on the discounted subtotal.

Run with `make run`; check with `make lint vet test`.
