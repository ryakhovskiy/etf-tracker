// Measure the actual CSS colour, fill opacity and ancestor group opacity over
// the painted solid surface. Used on card/chart surfaces (no background image).
export async function renderedContrast(locator, property = 'color') {
  return locator.evaluate((element, property) => {
    const context = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
    const rgba = value => {
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = value;
      context.fillRect(0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data].map((value, index) => index === 3 ? value / 255 : value);
    };
    const over = (front, back) => {
      const alpha = front[3] + back[3] * (1 - front[3]);
      return [...[0, 1, 2].map(index => alpha ? (front[index] * front[3] + back[index] * back[3] * (1 - front[3])) / alpha : 0), alpha];
    };
    const style = getComputedStyle(element);
    let foreground = rgba(style.getPropertyValue(property));
    if (property === 'fill') foreground[3] *= Number(style.fillOpacity);
    let background = [0, 0, 0, 0];
    const layers = [];
    for (let ancestor = element; ancestor; ancestor = ancestor.parentElement) {
      const computed = getComputedStyle(ancestor);
      const surface = rgba(computed.backgroundColor);
      foreground = over(foreground, surface);
      background = over(background, surface);
      const opacity = Number(computed.opacity);
      foreground[3] *= opacity;
      background[3] *= opacity;
      if (opacity !== 1 || surface[3] !== 0) layers.push({ tag: ancestor.tagName, opacity, background: computed.backgroundColor });
    }
    const pageBackground = rgba(getComputedStyle(document.documentElement).getPropertyValue('--bg'));
    foreground = over(foreground, pageBackground);
    background = over(background, pageBackground);
    const luminance = color => color.slice(0, 3).map(channel => channel / 255).map(channel => channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4).reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
    const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
    return { ratio: (values[0] + 0.05) / (values[1] + 0.05), foreground, background, layers };
  }, property);
}
