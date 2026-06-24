"""Secret-at-rest protection: KMS envelope encryption (SPEC §4.2, §8).

Kubeconfigs, and later API keys / the OIDC secret, are encrypted with a random
data key (DEK); the DEK is wrapped by a KMS key. Only ciphertext + wrapped DEK
are persisted — never plaintext, never the unwrapped DEK. Decryption happens in
memory at point of use.
"""
