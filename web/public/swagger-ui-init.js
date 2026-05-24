// Swagger UI initialisation. Referenced as a plain <script src="...">
// from the /api/v1/docs page so that Content-Security-Policy can use
// script-src 'self' without requiring 'unsafe-inline'.
window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "./openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    tryItOutEnabled: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset,
    ],
    layout: "StandaloneLayout",
  });
});
