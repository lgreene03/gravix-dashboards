# Gravix recipe: Rails
#
# There is no Gravix gem yet, so this uses Ruby's standard-library Net::HTTP
# against the documented REST contract. Everything below is what an
# ApplicationController around_action would do.
#
# Because there is no SDK, nothing sanitizes the path for you: translate your
# own route pattern to {name} form before sending. Sending "/users/1234" would
# be rejected, and sending a raw id as a dimension is what makes cardinality
# unbounded.
#
# Run:
#   GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) \
#     ruby client.rb
require "json"
require "net/http"
require "securerandom"
require "uri"

# Gravix requires a UUID version 7 — SecureRandom.uuid returns a v4, which is
# rejected with "event_id must be UUIDv7 (got v4)".
def uuid_v7
  now_ms = (Time.now.to_f * 1000).to_i
  bytes = SecureRandom.random_bytes(16).unpack("C*")
  6.times { |i| bytes[5 - i] = (now_ms >> (8 * i)) & 0xFF }
  bytes[6] = (bytes[6] & 0x0F) | 0x70  # version 7
  bytes[8] = (bytes[8] & 0x3F) | 0x80  # variant
  hex = bytes.map { |b| format("%02x", b) }.join
  [hex[0, 8], hex[8, 4], hex[12, 4], hex[16, 4], hex[20, 12]].join("-")
end

# In a controller this is around_action: start the clock, yield, then record.
def record_fact(method:, path_template:, status_code:, latency_ms:)
  endpoint = ENV.fetch("GRAVIX_ENDPOINT", "http://localhost:8090")
  uri = URI.parse("#{endpoint}/api/v1/facts")

  request = Net::HTTP::Post.new(uri)
  request["Content-Type"] = "application/json"
  request["X-API-Key"] = ENV.fetch("GRAVIX_API_KEY", "")
  request.body = JSON.generate(
    event_id: uuid_v7,
    event_time: Time.now.utc.strftime("%Y-%m-%dT%H:%M:%SZ"),
    service: "my-rails-app",
    method: method,
    path_template: path_template,
    status_code: status_code,
    latency_ms: latency_ms,
    user_agent_family: "ruby"
  )

  Net::HTTP.start(uri.hostname, uri.port, use_ssl: uri.scheme == "https") do |http|
    http.request(request)
  end
end

if __FILE__ == $PROGRAM_NAME
  # "/users/{id}", not the requested "/users/1234": the template is the route,
  # not the URL.
  response = record_fact(
    method: "GET",
    path_template: "/users/{id}",
    status_code: 200,
    latency_ms: 42
  )
  puts "gravix responded #{response.code}"
end
