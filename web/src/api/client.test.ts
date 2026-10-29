// Covers token parsing from the URL.
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { tokenFromHash } from './client.ts'

test('token is extracted from the hash', () => {
  assert.equal(tokenFromHash('#/?token=abc123'), 'abc123')
})

test('token stops at the next parameter', () => {
  assert.equal(tokenFromHash('#/?token=abc123&x=1'), 'abc123')
})

test('missing token gives an empty string', () => {
  assert.equal(tokenFromHash(''), '')
  assert.equal(tokenFromHash('#/sessions'), '')
})

test('empty token gives an empty string', () => {
  assert.equal(tokenFromHash('#/?token='), '')
})
