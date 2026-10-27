export interface ApiEnvelope<T = unknown> {
  code: number
  msg?: string
  data?: T
}

export interface ApiDataEnvelope<T> extends ApiEnvelope<T> {
  data: T
}

export interface SessionUser {
  id?: number
  username: string
}
