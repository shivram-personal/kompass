"""Provider abstraction + credential management (SPEC §4.4).

One adapter per provider shape; adding a provider is one adapter. Model lists
are fetched from the provider's models endpoint where available, with an
admin-editable fallback list (model names drift — never hardcode them).
"""
