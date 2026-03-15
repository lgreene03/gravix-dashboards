/** Error returned by the Gravix API. */
export class APIError extends Error {
    statusCode;
    constructor(message, statusCode) {
        super(message);
        this.name = "APIError";
        this.statusCode = statusCode;
    }
}
